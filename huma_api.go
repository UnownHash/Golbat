package main

import (
	"io"
	"net/http"

	"golbat/config"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
	gojson "github.com/goccy/go-json"
)

const securitySchemeName = "golbatSecret"

func newHumaConfig(version string) huma.Config {
	cfg := huma.DefaultConfig("Golbat API", version)

	goccyFmt := huma.Format{
		Marshal:   func(w io.Writer, v any) error { return gojson.NewEncoder(w).Encode(v) },
		Unmarshal: gojson.Unmarshal,
	}
	cfg.Formats = map[string]huma.Format{
		"application/json": goccyFmt,
		"json":             goccyFmt,
	}

	if cfg.Components == nil {
		cfg.Components = &huma.Components{}
	}
	if cfg.Components.SecuritySchemes == nil {
		cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	cfg.Components.SecuritySchemes[securitySchemeName] = &huma.SecurityScheme{
		Type: "apiKey",
		In:   "header",
		Name: "X-Golbat-Secret",
	}

	// Disable Huma's schema-link transformer (DefaultConfig adds it via CreateHooks).
	// It injects a `$schema` field into object responses and a `Link: rel="describedBy"`
	// header, which would diverge from the legacy v2/v3 wire format. The OpenAPI docs
	// at /docs and /openapi.json are unaffected — they come from the spec, not this
	// response transformer.
	cfg.CreateHooks = nil

	return cfg
}

// golbatSecretMiddleware enforces config.Config.ApiSecret as the X-Golbat-Secret
// header for any operation declaring the golbatSecret security requirement.
// Mirrors AuthRequired(): an empty configured secret disables auth.
func golbatSecretMiddleware(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		secret := config.Config.ApiSecret
		if secret == "" {
			next(ctx)
			return
		}
		requiresAuth := false
		for _, req := range ctx.Operation().Security {
			if _, ok := req[securitySchemeName]; ok {
				requiresAuth = true
				break
			}
		}
		if requiresAuth && ctx.Header("X-Golbat-Secret") != secret {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "invalid or missing X-Golbat-Secret")
			return
		}
		next(ctx)
	}
}

func setupHumaAPI(r *gin.Engine) huma.API {
	version := gitRevision
	if version == "" {
		version = "dev"
	}
	api := humagin.New(r, newHumaConfig(version))

	api.UseMiddleware(golbatSecretMiddleware(api))

	return api
}
