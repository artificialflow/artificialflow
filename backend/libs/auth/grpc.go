package auth

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// UnaryServerInterceptor authenticates gRPC calls and enforces ArtificialFlow roles.
func (m *Middleware) UnaryServerInterceptor(roles ...string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		cfg, err := m.provider.GetConfig(ctx)
		if err != nil {
			return nil, status.Error(codes.Unavailable, "authentication configuration unavailable")
		}
		if !cfg.Enabled() {
			return handler(WithPrincipal(ctx, disabledAuthPrincipal()), req)
		}

		rawAccessToken := bearerTokenFromMetadata(ctx)
		if rawAccessToken == "" {
			return nil, status.Error(codes.Unauthenticated, "authorization metadata required")
		}

		verifier, err := m.getVerifier(ctx, cfg)
		if err != nil {
			return nil, status.Error(codes.Unavailable, "authentication configuration unavailable")
		}
		principal, err := verifier.Verify(ctx, rawAccessToken)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}
		if err := resolvePrincipalTenant(principal); err != nil {
			return nil, status.Error(codes.Unauthenticated, "tenant claim required")
		}
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			requested := md.Get("x-tenant-id")
			if len(requested) > 0 && requested[0] != "" && requested[0] != principal.TenantID {
				return nil, status.Error(codes.InvalidArgument, "tenant conflict")
			}
		}
		if !principal.HasAnyRole(roles...) {
			return nil, status.Error(codes.PermissionDenied, "forbidden")
		}

		authedCtx := WithPrincipal(ctx, *principal)
		authedCtx = context.WithValue(authedCtx, "user_claims", principal.Claims)
		authedCtx = context.WithValue(authedCtx, "user_id", principal.Subject)
		return handler(authedCtx, req)
	}
}

func bearerTokenFromMetadata(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get("authorization")
	if len(values) == 0 {
		return ""
	}
	return extractBearerToken(values[0])
}
