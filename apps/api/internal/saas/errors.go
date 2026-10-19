package saas

import (
	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

// Error codes of the SaaS control module.
const (
	CodeModuleNotInPlan   = "module_not_in_plan"
	CodePlanLimitReached  = "plan_limit_reached"
	CodeInvoiceNotOpen    = "invoice_not_open"
	CodeTermsNotAccepted  = "terms_not_accepted"
	CodeSubscriptionNone  = "subscription_missing"
	CodeClosureState      = "closure_state_invalid"
	CodeSignatureInvalid  = "signature_invalid"
	CodeProviderUnknown   = "provider_unknown"
	CodeAmountMismatch    = "amount_mismatch"
	CodeSubscriptionState = "subscription_state_invalid"
	CodeSandbox           = "sandbox_not_billable"
	CodeTenantClosed      = "tenant_closed"
	// CodeClosureLinkNotFound answers a closure export link that is unknown, replaced or expired.
	CodeClosureLinkNotFound = "closure_link_not_found"
	// CodeExportNotReady answers a closure export link whose export cannot be downloaded.
	CodeExportNotReady = "export_not_ready"
)

func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

func errClosureLinkNotFound() error {
	return apperr.NewNotFound(CodeClosureLinkNotFound, "This download link is invalid or has expired")
}

func errNotFound(what string) error {
	return apperr.NewNotFound("not_found", what+" not found")
}

// ErrModuleNotInPlan is the answer to a request for a module the tenant's plan does not include.
func ErrModuleNotInPlan() error {
	return apperr.NewForbidden(CodeModuleNotInPlan, "This feature is not included in the store's plan")
}

func errLimitReached(key string) error {
	e := apperr.NewConflict(CodePlanLimitReached, "The plan limit is reached")
	e.Fields = map[string]string{"limit": key}
	return e
}

func errInvoiceNotOpen() error {
	return apperr.NewConflict(CodeInvoiceNotOpen, "The invoice is not open for payment")
}

func errTermsNotAccepted() error {
	return apperr.NewValidation(CodeTermsNotAccepted, "The terms must be accepted", map[string]string{"accept_terms": "required"})
}

func errNoSubscription() error {
	return apperr.NewConflict(CodeSubscriptionNone, "The store has no subscription")
}

func errClosureState(status string) error {
	return apperr.NewConflict(CodeClosureState, "The closure request is "+status)
}

func errSignature() error {
	return apperr.NewForbidden(CodeSignatureInvalid, "The payment callback signature is not valid")
}

func errProvider() error {
	return apperr.NewNotFound(CodeProviderUnknown, "Unknown payment provider")
}

func errAmountMismatch() error {
	return apperr.NewConflict(CodeAmountMismatch, "The paid amount differs from the invoice amount")
}

func errSubscriptionState(status string) error {
	return apperr.NewConflict(CodeSubscriptionState, "The subscription is "+status)
}

func errTenantClosed() error {
	return apperr.NewConflict(CodeTenantClosed, "The store was closed at its owner's request and cannot be reopened")
}

func errSandbox() error {
	return apperr.NewConflict(CodeSandbox, "A sandbox store has no subscription")
}

// CodePlanCodeTaken answers a plan code already in use.
const CodePlanCodeTaken = "plan_code_taken"

func errPlanCodeTaken() error {
	e := apperr.NewConflict(CodePlanCodeTaken, "A plan with this code exists")
	e.Fields = map[string]string{"code": "taken"}
	return e
}
