package rpcinterceptor_test

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	"connectrpc.com/connect"
	"github.com/emcfarlane/rpcinterceptor"
	"github.com/emcfarlane/rpcinterceptor/gen/examples"
	"github.com/emcfarlane/rpcinterceptor/gen/examples/examplesconnect"
)

type user struct {
	Name  string
	Roles []string
}

func Example_connectAuthorize() {
	users := map[string]*examples.User{
		"alice": {Name: "alice", Roles: []string{"admin"}},
		"bob":   {Name: "bob", Roles: []string{"user"}},
	}
	// Create an authorizer.
	authorizer, err := rpcinterceptor.NewAuthorizer(
		func(_ context.Context, md rpcinterceptor.Metadata) (*examples.User, error) {
			usernames := md.Get("username")
			if len(usernames) != 1 {
				return nil, fmt.Errorf("rpcinterceptor: authorization header not found")
			}
			user, ok := users[usernames[0]]
			if !ok {
				return nil, fmt.Errorf("rpcinterceptor: user not found")
			}
			fmt.Printf("user: %s with roles %s\n", user.Name, user.Roles)
			return user, nil
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	// Create a server.
	svc := &pingService{}
	mux := http.NewServeMux()
	mux.Handle(examplesconnect.NewPingServiceHandler(
		svc, connect.WithInterceptors(
			rpcinterceptor.Connect{authorizer},
		),
	))
	svr := httptest.NewServer(mux)
	defer svr.Close()

	// Create a client.
	client := examplesconnect.NewPingServiceClient(
		svr.Client(), svr.URL,
	)
	ctx := context.Background()
	// Call the service as alice and bob.
	for _, username := range []string{"bob", "alice"} {
		req := connect.NewRequest(
			&examples.PingRequest{
				Message: "hello, " + username,
			},
		)
		req.Header().Set("username", username)
		rsp, err := client.Ping(ctx, req)
		if err != nil {
			fmt.Printf("response: %v\n", err)
		} else {
			fmt.Printf("response: %s\n", rsp.Msg.Message)
		}
	}
	// Output:
	// user: bob with roles [user]
	// response: permission_denied: unauthorized
	// user: alice with roles [admin]
	// response: hello, alice
}

type pingService struct{}

func (p *pingService) Ping(ctx context.Context, req *connect.Request[examples.PingRequest]) (*connect.Response[examples.PingResponse], error) {
	return connect.NewResponse(
		&examples.PingResponse{
			Message: req.Msg.Message,
		},
	), nil
}
