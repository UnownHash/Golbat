package main

import (
	"context"
	"testing"

	"golbat/config"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	apiMethod = "/golbat_api.GolbatApi/ScanPokemon"
	rawMethod = "/raw_receiver.RawProto/SubmitRawProto"
)

// callInterceptor runs apiAuthUnaryInterceptor with a recording handler and
// reports whether the handler ran plus the status code (codes.OK on success).
func callInterceptor(t *testing.T, ctx context.Context, method string) (handled bool, code codes.Code) {
	t.Helper()
	info := &grpc.UnaryServerInfo{FullMethod: method}
	handler := func(ctx context.Context, req any) (any, error) {
		handled = true
		return "ok", nil
	}
	_, err := apiAuthUnaryInterceptor(ctx, nil, info, handler)
	return handled, status.Code(err)
}

func withMetadata(kv ...string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(kv...))
}

func TestApiAuthInterceptor(t *testing.T) {
	prev := config.Config.ApiSecret
	t.Cleanup(func() { config.Config.ApiSecret = prev })

	t.Run("no secret configured lets everything through", func(t *testing.T) {
		config.Config.ApiSecret = ""
		if handled, code := callInterceptor(t, context.Background(), apiMethod); !handled || code != codes.OK {
			t.Errorf("handled=%v code=%v, want handler run with OK", handled, code)
		}
	})

	config.Config.ApiSecret = "topsecret"

	cases := []struct {
		name    string
		ctx     context.Context
		method  string
		handled bool
		code    codes.Code
	}{
		{"missing metadata", context.Background(), apiMethod, false, codes.Unauthenticated},
		{"wrong x-golbat-secret", withMetadata("x-golbat-secret", "nope"), apiMethod, false, codes.Unauthenticated},
		{"wrong authorization", withMetadata("authorization", "Bearer nope"), apiMethod, false, codes.Unauthenticated},
		{"x-golbat-secret", withMetadata("x-golbat-secret", "topsecret"), apiMethod, true, codes.OK},
		{"authorization bare", withMetadata("authorization", "topsecret"), apiMethod, true, codes.OK},
		{"authorization bearer", withMetadata("authorization", "Bearer topsecret"), apiMethod, true, codes.OK},
		{"bearer prefix without the secret is not enough", withMetadata("authorization", "Bearer "), apiMethod, false, codes.Unauthenticated},
		{"raw service is not gated by the api secret", context.Background(), rawMethod, true, codes.OK},
		{"reflection is not gated", context.Background(), "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo", true, codes.OK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handled, code := callInterceptor(t, tc.ctx, tc.method)
			if handled != tc.handled || code != tc.code {
				t.Errorf("handled=%v code=%v, want handled=%v code=%v", handled, code, tc.handled, tc.code)
			}
		})
	}
}
