package rpcinterceptor

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// Connect is an interceptor that adapts the connect RPC framework to the
// rpcinterceptor framework.
type Connect struct {
	Interceptor Interceptor
}

func (c Connect) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		spec := req.Spec()
		msgIn, err := toMessage(req.Any())
		if err != nil {
			return nil, err
		}
		cstream := &connectUnaryStream{
			spec:     spec,
			in:       msgIn,
			metadata: connectMetadata(req.Header()),
		}
		// Build the stream chain.
		stream, err := c.Interceptor(ctx, cstream)
		if err != nil {
			return nil, asConnectErr(err)
		}
		if stream.IsClient() {
			if err := sendAndClose(stream, msgIn); err != nil {
				return nil, err
			}
		} else {
			if err := stream.Recv(msgIn); err != nil {
				return nil, err
			}
		}
		// Call the next handler in the chain.
		rsp, err := next(ctx, req)
		if err != nil {
			// TODO: error metadata
			return nil, err
		}
		// Capture response metadata.
		cstream.header = connectMetadata(rsp.Header())
		cstream.trailer = connectMetadata(rsp.Trailer())
		msgOut, err := toMessage(rsp.Any())
		if err != nil {
			return nil, err
		}
		if stream.IsClient() {
			if err := stream.Recv(msgOut); err != nil {
				return nil, err
			}
		} else {
			if err := sendAndClose(stream, msgOut); err != nil {
				return nil, err
			}
		}
		return rsp, nil
	}
}

func (c Connect) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		cstream := &connectClientStream{
			spec: spec,
			conn: conn,
		}
		stream, err := c.Interceptor(ctx, cstream)
		if err != nil {
			return nil // TODO: err?
		}
		return connectClientConn{
			StreamingClientConn: conn,
			stream:              stream,
		}
	}
}

func (c Connect) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		cstream := &connectServerStream{
			conn: conn,
		}
		stream, err := c.Interceptor(ctx, cstream)
		if err != nil {
			return err
		}
		if err := next(ctx, connectServerConn{
			StreamingHandlerConn: conn,
			stream:               stream,
		}); err != nil {
			return err
		}
		return stream.Close()
	}
}

// connectMetadata is a wrapper around http.Header that implements Metadata.
type connectMetadata map[string][]string

var _ Metadata = connectMetadata(nil)

func (c connectMetadata) Get(key string) []string {
	return http.Header(c).Values(key)
}
func (c connectMetadata) Set(key string, values ...string) {
	http.Header(c).Del(key)
	for _, value := range values {
		http.Header(c).Add(key, value)
	}
}
func (c connectMetadata) Append(key string, values ...string) {
	for _, value := range values {
		http.Header(c).Add(key, value)
	}
}
func (c connectMetadata) Range(f func(key, value string) bool) {
	for key, values := range c {
		for _, value := range values {
			if !f(key, value) {
				return
			}
		}
	}
}
func (c connectMetadata) Delete(key string) {
	http.Header(c).Del(key)
}
func (c connectMetadata) Len() int {
	return len(c)
}

type connectUnaryStream struct {
	spec     connect.Spec
	in, out  proto.Message
	next     connect.UnaryFunc
	metadata connectMetadata
	header   connectMetadata
	trailer  connectMetadata
}

var _ Stream = (*connectUnaryStream)(nil)

func (c *connectUnaryStream) IsClient() bool {
	return c.spec.IsClient
}
func (c *connectUnaryStream) Method() protoreflect.MethodDescriptor {
	return resolveMethod(protoregistry.GlobalFiles, c.spec.Procedure)
}
func (c *connectUnaryStream) Metadata() Metadata {
	return c.metadata
}
func (c *connectUnaryStream) Header() Metadata {
	return c.header
}
func (c *connectUnaryStream) Trailer() Metadata {
	return c.trailer
}
func (c *connectUnaryStream) Send(msg proto.Message) error {
	return nil // nop
}
func (c *connectUnaryStream) Close() error {
	return nil // nop
}
func (c *connectUnaryStream) Recv(msg proto.Message) error {
	return nil // nop
}

type connectClientStream struct {
	spec connect.Spec
	conn connect.StreamingClientConn
}

var _ Stream = (*connectClientStream)(nil)

func (c *connectClientStream) IsClient() bool {
	return c.spec.IsClient
}
func (c *connectClientStream) Method() protoreflect.MethodDescriptor {
	return resolveMethod(protoregistry.GlobalFiles, c.spec.Procedure)
}
func (c *connectClientStream) Metadata() Metadata {
	return connectMetadata(c.conn.RequestHeader())
}
func (c *connectClientStream) Header() Metadata {
	return connectMetadata(c.conn.ResponseHeader())
}
func (c *connectClientStream) Trailer() Metadata {
	return connectMetadata(c.conn.ResponseTrailer())
}
func (c *connectClientStream) Send(msg proto.Message) error {
	return c.conn.Send(msg)
}
func (c *connectClientStream) Close() error {
	return c.conn.CloseRequest()
}
func (c *connectClientStream) Recv(msg proto.Message) error {
	return c.conn.Receive(msg)
}

// connectClientConn is a wrapper around connect.Stream that implements
// connect.StreamingClientConn.
type connectClientConn struct {
	connect.StreamingClientConn

	stream Stream
}

var _ connect.StreamingClientConn = connectClientConn{}

func (c connectClientConn) Send(v any) error {
	msg, err := toMessage(v)
	if err != nil {
		return err
	}
	return c.stream.Send(msg)
}
func (c connectClientConn) CloseRequest() error {
	return c.stream.Close()
}
func (c connectClientConn) Receive(v any) error {
	msg, err := toMessage(v)
	if err != nil {
		return err
	}
	return c.stream.Send(msg)
}

type connectServerStream struct {
	conn connect.StreamingHandlerConn
}

var _ Stream = (*connectServerStream)(nil)

func (c *connectServerStream) IsClient() bool {
	return c.conn.Spec().IsClient
}
func (c *connectServerStream) Method() protoreflect.MethodDescriptor {
	return resolveMethod(protoregistry.GlobalFiles, c.conn.Spec().Procedure)
}
func (c *connectServerStream) Metadata() Metadata {
	return connectMetadata(c.conn.RequestHeader())
}
func (c *connectServerStream) Header() Metadata {
	return connectMetadata(c.conn.ResponseHeader())
}
func (c *connectServerStream) Trailer() Metadata {
	return connectMetadata(c.conn.ResponseTrailer())
}
func (c *connectServerStream) Send(msg proto.Message) error {
	return c.conn.Send(msg)
}
func (c *connectServerStream) Close() error {
	return nil
}
func (c *connectServerStream) Recv(msg proto.Message) error {
	return c.conn.Receive(msg)
}

type connectServerConn struct {
	connect.StreamingHandlerConn

	stream Stream
}

func (c connectServerConn) Receive(v any) error {
	msg, err := toMessage(v)
	if err != nil {
		return err
	}
	return c.stream.Recv(msg)
}
func (c connectServerConn) Send(v any) error {
	msg, err := toMessage(v)
	if err != nil {
		return err
	}
	return c.stream.Recv(msg)

}

func asConnectErr(err error) error {
	if err == nil {
		return nil
	}
	if ce := (*connect.Error)(nil); errors.As(err, &ce) {
		return ce
	}
	if ec := (*Error)(nil); errors.As(err, &ec) {
		return connect.NewError(connect.Code(ec.Code), ec)
	}
	return err
}
