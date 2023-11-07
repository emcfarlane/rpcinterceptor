rpcinterceptors
===

Build interceptors targeting the `Stream` interface and run them on connect-go
or grpc-go supporting both unary, streaming on clients and servers.

Authorize
---

Example interceptor takes a CEL expression to authorize a user. See [examples](authorize_test.go)
