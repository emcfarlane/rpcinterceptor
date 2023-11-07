package rpcinterceptor

import (
	"context"
	"fmt"
	"sync"

	"github.com/bufbuild/protovalidate-go/celext"
	authorizepb "github.com/emcfarlane/rpcinterceptor/gen/authorize"
	"github.com/google/cel-go/cel"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Authorizer is an interceptor that authorizes RPCs.
func NewAuthorizer[T proto.Message](authFn func(context.Context, Metadata) (T, error)) (Interceptor, error) {
	env, err := celext.DefaultEnv(true)
	if err != nil {
		return nil, err
	}
	typ := *new(T)
	env, err = env.Extend(
		cel.Types(typ),
		cel.Variable("method", cel.StringType),
		cel.Variable("user", getCelType(typ.ProtoReflect().Descriptor())),
		//cel.Variable("input", cel.AnyType), // input
	)
	if err != nil {
		return nil, err
	}
	bldr := &authorizeBuilder{
		env: env,
	}
	return func(ctx context.Context, next Stream) (Stream, error) {
		if next.IsClient() {
			// Clients are not checked.
			return next, nil
		}
		user, err := authFn(ctx, next.Metadata())
		if err != nil {
			return next, err
		}
		desc := next.Method()
		if desc == nil {
			return next, fmt.Errorf("rpcinterceptor: method descriptor not found")
		}
		prg := bldr.load(desc)
		ok, err := prg.eval(ctx, user)
		if err != nil {
			return next, err
		}
		if !ok {
			return nil, Errorf(7 /* PERMISSION_DENIED */, "unauthorized")
		}
		// All good.
		// TODO: interupt Recv to check.
		return next, nil
	}, nil
}

type authProgram struct {
	desc     protoreflect.MethodDescriptor
	programs []cel.Program
	err      error
}

func (a authProgram) eval(ctx context.Context, user proto.Message) (bool, error) {
	if a.err != nil {
		return false, a.err
	}
	for _, prg := range a.programs {
		out, _, err := prg.Eval(map[string]any{
			"method": a.desc.FullName(),
			"user":   user,
		})
		if err != nil {
			return false, err
		}
		if !out.Value().(bool) {
			return false, nil
		}
	}
	return true, nil
}

type authorizeBuilder struct {
	env *cel.Env

	mu    sync.Mutex
	cache map[protoreflect.MethodDescriptor]authProgram
}

func (a *authorizeBuilder) load(desc protoreflect.MethodDescriptor) authProgram {
	a.mu.Lock()
	defer a.mu.Unlock()
	if prg, ok := a.cache[desc]; ok {
		return prg
	}
	if a.cache == nil {
		a.cache = make(map[protoreflect.MethodDescriptor]authProgram)
	}
	prg, err := a.build(desc)
	aprg := authProgram{
		desc:     desc,
		programs: prg,
		err:      err,
	}
	a.cache[desc] = aprg
	return aprg

}
func (a *authorizeBuilder) build(desc protoreflect.MethodDescriptor) ([]cel.Program, error) {
	// Prepare CEL environment.
	if err := a.prepareEnvironment(desc); err != nil {
		return nil, err
	}
	// Lookup constraints for method.
	constraints := resolveMethodConstraints(desc)
	if constraints == nil {
		return nil, nil
	}
	var prgs []cel.Program
	for _, celContraints := range constraints.GetCel() {
		// Build CEL program.
		expression := celContraints.GetExpression()
		ast, iss := a.env.Compile(expression)
		if iss.Err() != nil {
			return nil, iss.Err()
		}
		prg, err := a.env.Program(ast)
		if err != nil {
			return nil, err
		}
		prgs = append(prgs, prg)
	}
	return prgs, nil
}

func (a *authorizeBuilder) prepareEnvironment(desc protoreflect.MethodDescriptor) error {
	env, err := a.env.Extend(
		cel.TypeDescs(desc.Parent().Parent()),
	)
	if err != nil {
		return err
	}
	a.env = env
	return nil
}

func resolveMethodConstraints(desc protoreflect.MethodDescriptor) *authorizepb.MethodConstraints {
	ext := proto.GetExtension(desc.Options(), authorizepb.E_Method)
	return ext.(*authorizepb.MethodConstraints)
}

func getCelType(fieldDesc protoreflect.MessageDescriptor) *cel.Type {
	switch fqn := fieldDesc.FullName(); fqn {
	case "google.protobuf.Any":
		return cel.AnyType
	case "google.protobuf.Duration":
		return cel.DurationType
	case "google.protobuf.Timestamp":
		return cel.TimestampType
	default:
		return cel.ObjectType(string(fqn))
	}
}
