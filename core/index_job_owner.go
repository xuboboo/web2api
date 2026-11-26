package core

// Job ownership.
//
// Index-check jobs are billed to one API key and carry that key's data (the
// URLs it is checking and the verdicts). Without an owner check any key could
// list every job, read another key's results, or cancel another key's batch —
// cross-tenant data exposure and a denial-of-service lever. The platform layer
// therefore binds the authenticated key id to the request context, and every job
// accessor filters by it.
//
// An empty owner means "admin" (JWT): admins may see and cancel everything.

import "context"

type jobOwnerContextKey struct{}
type jobUserContextKey struct{}
type consoleJobAccessContextKey struct{}

type consoleJobAccess struct {
	userID string
	admin  bool
}

// WithConsoleJobAccess is set only after the platform authenticates a user.
func WithConsoleJobAccess(ctx context.Context, userID string, admin bool) context.Context {
	return context.WithValue(ctx, consoleJobAccessContextKey{}, consoleJobAccess{userID: userID, admin: admin})
}

func consoleJobPrincipal(ctx context.Context) (consoleJobAccess, bool) {
	if ctx == nil {
		return consoleJobAccess{}, false
	}
	principal, ok := ctx.Value(consoleJobAccessContextKey{}).(consoleJobAccess)
	return principal, ok
}

// WithJobUser records the authenticated console submitter separately from the
// billing key. An empty user ID is never treated as a console identity.
func WithJobUser(ctx context.Context, userID string) context.Context {
	if ctx == nil || userID == "" {
		return ctx
	}
	return context.WithValue(ctx, jobUserContextKey{}, userID)
}

func jobUser(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	userID, _ := ctx.Value(jobUserContextKey{}).(string)
	return userID
}

// WithJobOwner binds the submitting API key id to ctx.
func WithJobOwner(ctx context.Context, keyID string) context.Context {
	if ctx == nil || keyID == "" {
		return ctx
	}
	return context.WithValue(ctx, jobOwnerContextKey{}, keyID)
}

// JobOwner returns the bound API key id, or "" for admin/anonymous callers.
func JobOwner(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if owner, ok := ctx.Value(jobOwnerContextKey{}).(string); ok {
		return owner
	}
	return ""
}

// jobVisibleTo reports whether the caller with ownerKeyID may see a job owned by
// jobOwnerKeyID. Admin (empty caller) sees everything; a key sees only its own.
func jobVisibleTo(jobOwnerKeyID, jobOwnerUserID, ownerKeyID string) bool {
	if ownerKeyID == "" {
		return true
	}
	return jobOwnerUserID == "" && jobOwnerKeyID == ownerKeyID
}
