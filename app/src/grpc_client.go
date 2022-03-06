package main

import (
	"flag"
	"fmt"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/resolver"
	echo "main/src/echo"
	"net"

	"log"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"

	"google.golang.org/grpc/admin"
	_ "google.golang.org/grpc/balancer/roundrobin"
	//_ "google.golang.org/grpc/resolver" // use for "dns:///be.cluster.local:50051"
	_ "google.golang.org/grpc/xds" // use for xds-experimental:///be-srv
)

// for round-robin LB
// https://github.com/grpc/grpc-go/tree/master/examples/features/load_balancing

const (
	exampleScheme      = "example"
	exampleServiceName = "lb.example.grpc.io"
)

var addrs = []string{"localhost:50051", "localhost:50052"}

type exampleResolverBuilder struct{}

func (*exampleResolverBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	r := &exampleResolver{
		target: target,
		cc:     cc,
		addrsStore: map[string][]string{
			exampleServiceName: addrs,
		},
	}
	r.start()
	return r, nil
}
func (*exampleResolverBuilder) Scheme() string { return exampleScheme }

type exampleResolver struct {
	target     resolver.Target
	cc         resolver.ClientConn
	addrsStore map[string][]string
}

func (r *exampleResolver) start() {
	addrStrs := r.addrsStore[r.target.Endpoint]
	addrs := make([]resolver.Address, len(addrStrs))
	for i, s := range addrStrs {
		addrs[i] = resolver.Address{Addr: s}
	}
	r.cc.UpdateState(resolver.State{Addresses: addrs})
}
func (*exampleResolver) ResolveNow(o resolver.ResolveNowOptions) {}
func (*exampleResolver) Close()                                  {}

func init() {
	resolver.Register(&exampleResolverBuilder{})
}

/////////////////////////////////////

func main() {

	address := flag.String("host", "dns:///localhost:50051", "dns:///localhost:50051 or xds-experimental:///be-srv")
	flag.Parse()

	//address = fmt.Sprintf("xds-experimental:///be-srv")

	// (optional) start background grpc admin services to monitor client
	// "google.golang.org/grpc/admin"
	go func() {
		lis, err := net.Listen("tcp", ":50053")
		if err != nil {
			log.Fatalf("failed to listen: %v", err)
		}
		defer lis.Close()
		opts := []grpc.ServerOption{grpc.MaxConcurrentStreams(10)}
		grpcServer := grpc.NewServer(opts...)
		cleanup, err := admin.Register(grpcServer)
		if err != nil {
			log.Fatalf("failed to register admin services: %v", err)
		}
		defer cleanup()

		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	// pick first
	pickFirstConn, err := grpc.Dial(
		fmt.Sprintf("%s:///%s", exampleScheme, exampleServiceName),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer pickFirstConn.Close()

	// round robin
	roundRobinConn, err := grpc.Dial(
		fmt.Sprintf("%s:///%s", exampleScheme, exampleServiceName),
		grpc.WithDefaultServiceConfig(`{"loadBalancingConfig": [{"round_robin":{}}]}`), // This sets the initial balancing policy.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer roundRobinConn.Close()

	// for xDS lb
	lookAsideConn, err := grpc.Dial(*address, grpc.WithInsecure())

	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer lookAsideConn.Close()

	c := echo.NewEchoServerClient(lookAsideConn)
	ctx := context.Background()

	for i := 0; i < 199; i++ {
		r, err := c.SayHello(ctx, &echo.EchoRequest{Name: "ping!"})
		if err != nil {
			log.Fatalf("could not greet: %v", err)
		}
		log.Printf("RPC Response: %v %v", i, r)
		time.Sleep(1 * time.Second)
	}
}
