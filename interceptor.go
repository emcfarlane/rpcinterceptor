package rpcinterceptor

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

type Metadata interface {
	Get(key string) []string
	Set(key string, values ...string)
	Append(key string, values ...string)
	Range(func(key, value string) bool)
	Delete(key string)
	Len() int
}

type Stream interface {
	// IsClient returns true if the stream is a client stream.
	IsClient() bool
	// Method returns the method descriptor of the RPC being invoked. If the
	// method descriptor is not known, Method returns nil.
	Method() protoreflect.MethodDescriptor

	// Metadata returns the metadata associated with the RPC.
	Metadata() Metadata
	// Header returns the header metadata replied for the RPC.
	Header() Metadata
	// Trailer returns the trailing metadata replied for the RPC.
	Trailer() Metadata

	// Recv receives a message.
	Recv(msg proto.Message) error

	// Send sends a message.
	Send(msg proto.Message) error
	// Close closes the send stream.
	Close() error
}

type Interceptor func(context.Context, Stream) (Stream, error)

func Chain(interceptors ...Interceptor) Interceptor {
	return func(ctx context.Context, next Stream) (Stream, error) {
		for _, interceptor := range interceptors {
			var err error
			next, err = interceptor(ctx, next)
			if err != nil {
				return nil, err
			}
		}
		return next, nil
	}
}

func toMessage(msg any) (proto.Message, error) {
	m, ok := msg.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("message is not a proto message")
	}
	return m, nil
}

func sendAndClose(stream Stream, msg proto.Message) error {
	if err := stream.Send(msg); err != nil {
		return err
	}
	return stream.Close()
}

func resolveMethod(files *protoregistry.Files, fullMethod string) protoreflect.MethodDescriptor {
	methodName := strings.ReplaceAll(
		strings.TrimPrefix(fullMethod, "/"),
		"/", ".",
	)
	desc, err := files.FindDescriptorByName(
		protoreflect.FullName(methodName),
	)
	if err != nil {
		return nil // could not find descriptor
	}
	methodDesc, ok := desc.(protoreflect.MethodDescriptor)
	if !ok {
		return nil // resolved descriptor is not a method
	}
	return methodDesc
}
