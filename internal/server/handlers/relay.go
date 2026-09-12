package handlers

import (
	"net/http"

	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/looplj/axonhub/llm"
)

func init() {
	router.NewGroupRouter("/v1").
		Use(middleware.APIKeyAuth()).
		AddRoute(
			router.NewRoute("/chat/completions", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAIChatCompletion)),
		).
		AddRoute(
			router.NewRoute("/responses", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAIResponse)),
		).
		AddRoute(
			router.NewRoute("/messages", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatAnthropicMessage)),
		).
		// added 20260908: image generation/edit passthrough for Hermes WebUI media endpoint.
		// The axonhub llm lib has no APIFormat for images, so relay a raw HTTP body:
		// same-protocol upstreams only, JSON passthrough, model name rewrites and
		// channel/group selection all reused from relay.Forward via the OpenAI
		// chat inbound (its metadata parser only reads model/stream fields).
		AddRoute(
			router.NewRoute("/images/generations", http.MethodPost).
				Handle(relay.ForwardImage("generations")),
		).
		AddRoute(
			router.NewRoute("/images/edits", http.MethodPost).
				Handle(relay.ForwardImage("edits")),
		)
}
