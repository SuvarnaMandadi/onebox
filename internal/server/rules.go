package server

import "context"

// ruleAllows checks an action-level rule (create, or list/view when not
// owner-scoped). Admins always pass; RuleOwner falls back to "must be
// authenticated" here since row-level ownership isn't known yet at this
// point (see ruleAllowsRecord and the list handler's owner-filter case).
func ruleAllows(ctx context.Context, kind RuleKind) bool {
	if _, ok := authAdminID(ctx); ok {
		return true
	}
	switch kind {
	case RulePublic:
		return true
	case RuleAuthenticated, RuleOwner:
		_, ok := authUserID(ctx)
		return ok
	default:
		return false
	}
}

// ruleAllowsRecord checks a rule against a specific already-fetched
// record's owner_id (view/update/delete).
func ruleAllowsRecord(ctx context.Context, kind RuleKind, ownerID any) bool {
	if _, ok := authAdminID(ctx); ok {
		return true
	}
	switch kind {
	case RulePublic:
		return true
	case RuleAuthenticated:
		_, ok := authUserID(ctx)
		return ok
	case RuleOwner:
		uid, ok := authUserID(ctx)
		if !ok {
			return false
		}
		owner, _ := ownerID.(string)
		return owner != "" && owner == uid
	default:
		return false
	}
}
