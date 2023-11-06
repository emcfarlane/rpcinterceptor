package rpcinterceptor

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// Refs:
// - Metadata https://github.com/grpc/grpc-go/blob/master/Documentation/grpc-metadata.md

// GRPC is an interceptor that adapts the grpc framework to the rpcinterceptor
// framework.
type GRPC struct {
	Interceptor Interceptor
}

func (g *GRPC) UnaryServer() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		md, _ := metadata.FromIncomingContext(ctx)
		gstream := &grpcUnaryStream{
			fullMethod: info.FullMethod,
			isClient:   false,
			metadata:   grpcMetadata(md),
		}
		stream, err := g.Interceptor(ctx, gstream)
		if err != nil {
			return nil, err
		}
		msgIn, err := toMessage(req)
		if err != nil {
			return nil, err
		}
		if err := stream.Recv(msgIn); err != nil {
			return nil, err
		}
		rsp, err := handler(ctx, req)
		if err != nil {
			return nil, err
		}
		outMD, _ := metadata.FromOutgoingContext(ctx)
		gstream.header = grpcMetadata(outMD)
		gstream.trailer = nil // TODO
		msgOut, err := toMessage(rsp)
		if err != nil {
			return nil, err
		}
		if err := sendAndClose(stream, msgOut); err != nil {
			return nil, err
		}
		return rsp, nil
	}
}
func (g *GRPC) UnaryClient() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req any, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		// TODO: metadata for client, need to do
		md, _ := metadata.FromOutgoingContext(ctx)
		gstream := &grpcUnaryStream{
			fullMethod: method,
			isClient:   true,
			metadata:   grpcMetadata(md),
		}
		stream, err := g.Interceptor(ctx, gstream)
		if err != nil {
			return err
		}
		msgIn, err := toMessage(req)
		if err != nil {
			return err
		}
		if err := stream.Send(msgIn); err != nil {
			return err
		}
		if err := stream.Close(); err != nil {
			return err
		}
		var header, trailer metadata.MD
		opts = append(opts, grpc.Header(&header), grpc.Trailer(&trailer))
		if err := invoker(ctx, method, req, reply, cc, opts...); err != nil {
			return err
		}
		gstream.header = grpcMetadata(header)
		gstream.trailer = grpcMetadata(trailer)
		msgOut, err := toMessage(reply)
		if err != nil {
			return err
		}
		if err := stream.Recv(msgOut); err != nil {
			return err
		}
		return nil
	}
}

func (g *GRPC) StreamServer() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		md, _ := metadata.FromIncomingContext(ss.Context())
		gstream := &grpcServerStream{
			conn:       ss,
			fullMethod: info.FullMethod,
			metadata:   grpcMetadata(md),
		}
		stream, err := g.Interceptor(ss.Context(), gstream)
		if err != nil {
			return err
		}
		return handler(srv, grpcServerConn{
			conn:   ss,
			stream: stream,
		})
	}
}
func (g *GRPC) StreamClient() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		// ...
		conn, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil {
			return nil, err
		}
		md, _ := metadata.FromOutgoingContext(ctx)
		gstream := &grpcClientStream{
			conn:       conn,
			fullMethod: method,
			metadata:   grpcMetadata(md),
		}
		stream, err := g.Interceptor(ctx, gstream)
		if err != nil {
			return nil, err
		}
		return grpcClientConn{
			conn:   conn,
			stream: stream,
		}, nil
	}
}

type grpcMetadata map[string][]string

var _ Metadata = grpcMetadata(nil)

func (g grpcMetadata) Get(key string) []string {
	return metadata.MD(g).Get(key)
}
func (g grpcMetadata) Set(key string, values ...string) {
	metadata.MD(g).Set(key, values...)
}
func (g grpcMetadata) Append(key string, values ...string) {
	metadata.MD(g).Append(key, values...)
}
func (g grpcMetadata) Range(f func(key, value string) bool) {
	for key, values := range g {
		for _, value := range values {
			if !f(key, value) {
				return
			}
		}
	}
}
func (g grpcMetadata) Delete(key string) {
	metadata.MD(g).Delete(key)
}
func (g grpcMetadata) Len() int {
	return len(g)
}

type grpcUnaryStream struct {
	fullMethod string
	isClient   bool
	metadata   Metadata
	header     Metadata
	trailer    Metadata
}

var _ Stream = (*grpcUnaryStream)(nil)

func (g *grpcUnaryStream) IsClient() bool {
	return g.isClient
}
func (g *grpcUnaryStream) Method() protoreflect.MethodDescriptor {
	return resolveMethod(protoregistry.GlobalFiles, g.fullMethod)
}
func (g *grpcUnaryStream) Metadata() Metadata {
	return g.metadata
}
func (g *grpcUnaryStream) Header() Metadata {
	return g.header
}
func (g *grpcUnaryStream) Trailer() Metadata {
	return g.trailer
}
func (g *grpcUnaryStream) Send(msg proto.Message) error {
	return nil // nop
}
func (g *grpcUnaryStream) Close() error {
	return nil // nop
}
func (g *grpcUnaryStream) Recv(msg proto.Message) error {
	return nil // nop
}

type grpcServerStream struct {
	conn       grpc.ServerStream
	fullMethod string
	metadata   Metadata
}

var _ Stream = (*grpcServerStream)(nil)

func (g *grpcServerStream) IsClient() bool {
	return false
}
func (g *grpcServerStream) Method() protoreflect.MethodDescriptor {
	return resolveMethod(protoregistry.GlobalFiles, g.fullMethod)
}
func (g *grpcServerStream) Metadata() Metadata {
	return g.metadata
}
func (g *grpcServerStream) Header() Metadata {
	return nil // TODO: call set header on stream
}
func (g *grpcServerStream) Trailer() Metadata {
	return nil // TODO: call set trailer on stream
}
func (g *grpcServerStream) Send(msg proto.Message) error {
	return g.conn.SendMsg(msg)
}
func (g *grpcServerStream) Close() error {
	return nil // nop
}
func (g *grpcServerStream) Recv(msg proto.Message) error {
	return g.conn.RecvMsg(msg)
}

type grpcClientStream struct {
	conn       grpc.ClientStream
	fullMethod string
	metadata   Metadata
}

var _ Stream = (*grpcClientStream)(nil)

func (g *grpcClientStream) IsClient() bool {
	return false
}
func (g *grpcClientStream) Method() protoreflect.MethodDescriptor {
	return resolveMethod(protoregistry.GlobalFiles, g.fullMethod)
}
func (g *grpcClientStream) Metadata() Metadata {
	return g.metadata
}
func (g *grpcClientStream) Header() Metadata {
	hdr, _ := g.conn.Header()
	return grpcMetadata(hdr)
}
func (g *grpcClientStream) Trailer() Metadata {
	return grpcMetadata(g.conn.Trailer())
}
func (g *grpcClientStream) Send(msg proto.Message) error {
	return g.conn.SendMsg(msg)
}
func (g *grpcClientStream) Close() error {
	return g.conn.CloseSend()
}
func (g *grpcClientStream) Recv(msg proto.Message) error {
	return g.conn.RecvMsg(msg)
}

type grpcServerConn struct {
	conn   grpc.ServerStream
	stream Stream
}

var _ grpc.ServerStream = grpcServerConn{}

func (g grpcServerConn) SetHeader(md metadata.MD) error {
	header := g.stream.Header()
	for key, vals := range md {
		header.Append(key, vals...)
	}
	return g.conn.SetHeader(nil)
}
func (g grpcServerConn) SendHeader(md metadata.MD) error {
	header := g.stream.Header()
	for key, vals := range md {
		header.Append(key, vals...)
	}
	return g.conn.SendHeader(nil)
}
func (g grpcServerConn) SetTrailer(md metadata.MD) {
	trailer := g.stream.Trailer()
	for key, vals := range md {
		trailer.Append(key, vals...)
	}
}
func (g grpcServerConn) Context() context.Context {
	return g.conn.Context()
}
func (g grpcServerConn) SendMsg(v any) error {
	msg, err := toMessage(v)
	if err != nil {
		return err
	}
	return g.stream.Send(msg)
}
func (g grpcServerConn) RecvMsg(v any) error {
	msg, err := toMessage(v)
	if err != nil {
		return err
	}
	return g.stream.Recv(msg)
}

type grpcClientConn struct {
	conn   grpc.ClientStream
	stream Stream
}

var _ grpc.ClientStream = grpcClientConn{}

func (g grpcClientConn) Header() (metadata.MD, error) {
	return g.conn.Header()
}
func (g grpcClientConn) Trailer() metadata.MD {
	return g.conn.Trailer()
}
func (g grpcClientConn) CloseSend() error {
	return g.stream.Close()
}
func (g grpcClientConn) Context() context.Context {
	return g.conn.Context()
}
func (g grpcClientConn) SendMsg(m any) error {
	msg, err := toMessage(m)
	if err != nil {
		return err
	}
	return g.stream.Send(msg)
}
func (g grpcClientConn) RecvMsg(m any) error {
	msg, err := toMessage(m)
	if err != nil {
		return err
	}
	return g.stream.Recv(msg)
}
