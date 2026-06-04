package agentgw

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/nodehive/gpu-platform/internal/nodes"
)

const connectMethod = "/nodehive.agent.v1.AgentService/Connect"

type authInfo struct {
	NodeID uuid.UUID
	OrgID  uuid.UUID
}

type ctxKey struct{}

func authFromContext(ctx context.Context) (authInfo, bool) {
	ai, ok := ctx.Value(ctxKey{}).(authInfo)
	return ai, ok
}

// wrappedStream lets the interceptor hand the handler a context carrying authInfo.
type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }

// StreamAuthInterceptor authenticates the agent's bearer credential on Connect and
// injects the resolved node identity into the stream context. Enroll is a unary RPC
// and is intentionally not covered (it has no credential yet).
func StreamAuthInterceptor(svc *nodes.Service) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if info.FullMethod != connectMethod {
			return handler(srv, ss)
		}
		md, _ := metadata.FromIncomingContext(ss.Context())
		cred := bearer(md)
		if cred == "" {
			return status.Error(codes.Unauthenticated, "missing agent credential")
		}
		nodeID, orgID, err := svc.AuthenticateAgent(ss.Context(), cred)
		if err != nil {
			return status.Error(codes.Unauthenticated, "invalid agent credential")
		}
		ctx := context.WithValue(ss.Context(), ctxKey{}, authInfo{NodeID: nodeID, OrgID: orgID})
		return handler(srv, &wrappedStream{ServerStream: ss, ctx: ctx})
	}
}

func bearer(md metadata.MD) string {
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(vals[0], "Bearer "))
}
