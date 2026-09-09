package main

import (
	"context"
	"crypto/subtle"
	"strings"

	"golbat/config"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// grpcApiServicePrefix is the full-method prefix of every GolbatApi RPC.
const grpcApiServicePrefix = "/golbat_api.GolbatApi/"

// grpcApiSecretMetadataKey is the canonical metadata key for the API secret:
// the same name as the HTTP header the JSON API checks.
const grpcApiSecretMetadataKey = "x-golbat-secret"

// apiSecretAllows enforces config.Config.ApiSecret — the same api_secret the
// HTTP X-Golbat-Secret header carries — on GolbatApi methods (matched by the
// /golbat_api.GolbatApi/ full-method prefix). Raw ingest keeps its own
// raw_bearer check inside SubmitRawProto, and the reflection service is open
// (it only exposes the schema). An empty secret disables the check,
// mirroring golbatSecretMiddleware. Returns nil to allow the call, or the
// Unauthenticated status error to reject it.
func apiSecretAllows(ctx context.Context, fullMethod string) error {
	if strings.HasPrefix(fullMethod, grpcApiServicePrefix) {
		if secret := config.Config.ApiSecret; secret != "" {
			md, _ := metadata.FromIncomingContext(ctx)
			if !metadataCarriesApiSecret(md, secret) {
				return status.Error(codes.Unauthenticated, "invalid or missing api secret")
			}
		}
	}
	return nil
}

// apiAuthUnaryInterceptor applies apiSecretAllows to unary GolbatApi RPCs.
func apiAuthUnaryInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if err := apiSecretAllows(ctx, info.FullMethod); err != nil {
		return nil, err
	}
	return handler(ctx, req)
}

// apiAuthStreamInterceptor applies the same rule to streaming RPCs, so a
// future streaming method on GolbatApi cannot be unauthenticated by omission.
func apiAuthStreamInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if err := apiSecretAllows(ss.Context(), info.FullMethod); err != nil {
		return err
	}
	return handler(srv, ss)
}

// metadataCarriesApiSecret accepts x-golbat-secret: <secret>,
// authorization: <secret>, or authorization: Bearer <secret>.
func metadataCarriesApiSecret(md metadata.MD, secret string) bool {
	for _, v := range md.Get(grpcApiSecretMetadataKey) {
		if secretEqual(v, secret) {
			return true
		}
	}
	for _, v := range md.Get("authorization") {
		if secretEqual(strings.TrimPrefix(v, "Bearer "), secret) {
			return true
		}
	}
	return false
}

func secretEqual(candidate, secret string) bool {
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(secret)) == 1
}
