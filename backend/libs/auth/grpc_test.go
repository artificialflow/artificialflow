package auth

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type fakeTokenVerifier struct {
	principal Principal
	err       error
}

func (v fakeTokenVerifier) Verify(_ context.Context, _ string) (*Principal, error) {
	if v.err != nil {
		return nil, v.err
	}
	return &v.principal, nil
}

func testGRPCMiddleware(principal Principal) *Middleware {
	cfg := Config{
		InternalIssuerURL:   "http://issuer.local",
		ClientID:            "workflow-backend",
		TokenValidationMode: TokenModeJWT,
	}
	return &Middleware{
		provider:       StaticConfigProvider{Config: cfg},
		verifier:       fakeTokenVerifier{principal: principal},
		verifierConfig: configFingerprint(cfg),
	}
}

func TestUnaryServerInterceptorRequiresAuthorizationMetadata(t *testing.T) {
	interceptor := testGRPCMiddleware(Principal{Roles: []string{RoleAdmin}}).UnaryServerInterceptor(RoleAdmin)

	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}, func(context.Context, any) (any, error) {
		t.Fatal("handler should not be called without authorization metadata")
		return nil, nil
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated, got %v: %v", status.Code(err), err)
	}
}

func TestUnaryServerInterceptorRejectsMissingRole(t *testing.T) {
	interceptor := testGRPCMiddleware(Principal{Subject: "accountant", Roles: []string{"accountant"}}).UnaryServerInterceptor(RoleAdmin)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer token"))

	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}, func(context.Context, any) (any, error) {
		t.Fatal("handler should not be called for missing role")
		return nil, nil
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected permission denied, got %v: %v", status.Code(err), err)
	}
}

func TestUnaryServerInterceptorAddsPrincipalToContext(t *testing.T) {
	interceptor := testGRPCMiddleware(Principal{Subject: "client-1", Roles: []string{RoleClient}}).UnaryServerInterceptor(RoleAdmin, RoleClient)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer token"))

	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}, func(ctx context.Context, _ any) (any, error) {
		principal, ok := PrincipalFromContext(ctx)
		if !ok {
			t.Fatal("expected principal in context")
		}
		if principal.Subject != "client-1" {
			t.Fatalf("expected subject client-1, got %q", principal.Subject)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("expected successful call, got %v", err)
	}
}

func TestUnaryServerInterceptorDisabledAuthAddsLocalPrincipal(t *testing.T) {
	middleware := &Middleware{
		provider: StaticConfigProvider{Config: Config{TokenValidationMode: TokenModeJWT}},
	}
	interceptor := middleware.UnaryServerInterceptor(RoleAdmin)

	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}, func(ctx context.Context, _ any) (any, error) {
		principal, ok := PrincipalFromContext(ctx)
		if !ok {
			t.Fatal("expected disabled-auth principal in context")
		}
		if !principal.HasRole(RoleAdmin) {
			t.Fatal("expected disabled-auth principal to have admin role")
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("expected successful disabled-auth call, got %v", err)
	}
}
