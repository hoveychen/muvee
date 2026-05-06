package api

import "testing"

// attachEmbedBridge is the helper applied to every project / tunnel /
// domain-only router emitted from handleTraefikConfig. The contract:
//   - appends "embed-bridge@file" to router.Middlewares
//   - keeps any pre-existing middlewares (forwardAuth) ahead of it
//   - is idempotent: calling twice does not double-append
func TestAttachEmbedBridge_AppendToEmptyChain(t *testing.T) {
	r := traefikRouter{}
	attachEmbedBridge(&r)
	if got := r.Middlewares; len(got) != 1 || got[0] != embedBridgeMiddlewareRef {
		t.Errorf("got %v want [%s]", got, embedBridgeMiddlewareRef)
	}
}

func TestAttachEmbedBridge_AppendsAfterExisting(t *testing.T) {
	r := traefikRouter{Middlewares: []string{"myapp-auth"}}
	attachEmbedBridge(&r)
	if got := r.Middlewares; len(got) != 2 || got[0] != "myapp-auth" || got[1] != embedBridgeMiddlewareRef {
		t.Errorf("expected forwardAuth then embed-bridge; got %v", got)
	}
}

func TestAttachEmbedBridge_Idempotent(t *testing.T) {
	r := traefikRouter{}
	attachEmbedBridge(&r)
	attachEmbedBridge(&r)
	attachEmbedBridge(&r)
	if got := r.Middlewares; len(got) != 1 || got[0] != embedBridgeMiddlewareRef {
		t.Errorf("idempotency broken: got %v", got)
	}
}
