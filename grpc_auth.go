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

// apiAuthUnaryInterceptor enforces config.Config.ApiSecret — the same
// api_secret the HTTP X-Golbat-Secret header carries — on GolbatApi methods.
// Raw ingest keeps its own raw_bearer check inside SubmitRawProto, and the
// reflection service is open (it only exposes the schema). An empty secret
// disables the check, mirroring golbatSecretMiddleware.
func apiAuthUnaryInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if strings.HasPrefix(info.FullMethod, grpcApiServicePrefix) {
		if secret := config.Config.ApiSecret; secret != "" {
			md, _ := metadata.FromIncomingContext(ctx)
			if !metadataCarriesApiSecret(md, secret) {
				return nil, status.Error(codes.Unauthenticated, "invalid or missing api secret")
			}
		}
	}
	return handler(ctx, req)
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
