package authn

import (
	"context"

	coreauthn "github.com/OpenNSW/core/authn"
)

// UserProfileService resolves an authenticated caller to a persisted user
// record, and is called on a user's first appearance after the token validates.
// It is optional — pass nil to NewManager to skip user persistence, in which
// case Principal.UserID stays empty.
//
// Taking a *Principal rather than a list of named identity values means adding
// a claim never changes this signature. Implementations must not mutate the
// principal: the request's context already shares it.
type UserProfileService interface {
	// GetOrCreateUser returns the persisted user ID for p, creating the record
	// if this is the caller's first login. It should be idempotent. An error is
	// logged by core/authn but does not by itself reject the request.
	GetOrCreateUser(ctx context.Context, p *Principal) (string, error)
}

// coreProfileShim adapts a first-party UserProfileService to the interface
// core/authn calls, converting core's principal on the way through so
// implementations never see a core type.
type coreProfileShim struct {
	svc UserProfileService
}

func (s coreProfileShim) GetOrCreateUser(ctx context.Context, up *coreauthn.UserPrincipal) (string, error) {
	return s.svc.GetOrCreateUser(ctx, principalFromUserPrincipal(up))
}

// principalFromUserPrincipal maps the principal core/authn passes to
// UserProfileService. UserID is left empty: resolving it is precisely what the
// service being called is for.
func principalFromUserPrincipal(up *coreauthn.UserPrincipal) *Principal {
	if up == nil {
		return nil
	}
	return &Principal{
		Kind:        KindUser,
		IDPUserID:   up.Subject,
		Roles:       up.Roles,
		Scopes:      up.Scopes,
		Email:       up.ExtraClaims.String(claimEmail),
		PhoneNumber: up.ExtraClaims.String(claimPhoneNumber),
		OUID:        up.ExtraClaims.String(claimOUID),
		OUHandle:    up.ExtraClaims.String(claimOUHandle),
		ExtraClaims: extraClaims(up.ExtraClaims, builtinUserClaimNames),
	}
}
