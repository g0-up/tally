package cash

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

type fundDTO struct {
	ID             uuid.UUID   `json:"id"`
	Kind           string      `json:"kind"`
	Name           string      `json:"name"`
	BankName       *string     `json:"bank_name"`
	AccountNo      *string     `json:"account_no"`
	OpeningBalance money.Money `json:"opening_balance"`
	OpeningDate    *string     `json:"opening_date"`
	Balance        money.Money `json:"balance"`
	Active         bool        `json:"active"`
	Version        int         `json:"version"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

func toFundDTO(f FundAccount) fundDTO {
	return fundDTO{
		ID: f.ID, Kind: f.Kind, Name: f.Name, BankName: f.BankName, AccountNo: f.AccountNo,
		OpeningBalance: f.OpeningBalance, OpeningDate: dateString(f.OpeningDate), Balance: f.Balance,
		Active: f.Active, Version: f.Version, UpdatedAt: f.UpdatedAt,
	}
}

type methodDTO struct {
	ID                   uuid.UUID `json:"id"`
	Code                 string    `json:"code"`
	Name                 string    `json:"name"`
	FundAccountID        uuid.UUID `json:"fund_account_id"`
	FundKind             string    `json:"fund_kind"`
	RequiresVerification bool      `json:"requires_verification"`
	Active               bool      `json:"active"`
}

func toMethodDTO(m PaymentMethod) methodDTO {
	return methodDTO{
		ID: m.ID, Code: m.Code, Name: m.Name, FundAccountID: m.FundAccountID, FundKind: m.FundKind,
		RequiresVerification: m.RequiresVerification, Active: m.Active,
	}
}

// fundPatch is the PATCH /funds/{id} body. Clearable fields keep their raw JSON so null (clear)
// and absent (unchanged) differ.
type fundPatch struct {
	Name           *string         `json:"name"`
	BankName       json.RawMessage `json:"bank_name"`
	AccountNo      json.RawMessage `json:"account_no"`
	OpeningBalance *money.Money    `json:"opening_balance"`
	OpeningDate    json.RawMessage `json:"opening_date"`
}

func (p fundPatch) input() (FundInput, error) {
	in := FundInput{Name: p.Name, OpeningBalance: p.OpeningBalance}
	fields := map[string]string{}
	var ok bool
	if in.BankName, ok = optString(p.BankName); !ok {
		fields["bank_name"] = "invalid_type"
	}
	if in.AccountNo, ok = optString(p.AccountNo); !ok {
		fields["account_no"] = "invalid_type"
	}
	if p.OpeningDate != nil {
		in.OpeningDate.Set = true
		if !isNull(p.OpeningDate) {
			var s string
			if err := json.Unmarshal(p.OpeningDate, &s); err != nil {
				fields["opening_date"] = "invalid_date"
			} else if t, err := time.Parse(time.DateOnly, s); err != nil {
				fields["opening_date"] = "invalid_date"
			} else {
				d := clock.Date(t.Year(), t.Month(), t.Day())
				in.OpeningDate.Value = &d
			}
		}
	}
	if len(fields) > 0 {
		return FundInput{}, errInvalid(fields)
	}
	return in, nil
}

func isNull(raw json.RawMessage) bool { return string(bytes.TrimSpace(raw)) == "null" }

func optString(raw json.RawMessage) (Opt[string], bool) {
	if raw == nil {
		return Opt[string]{}, true
	}
	if isNull(raw) {
		return Opt[string]{Set: true}, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return Opt[string]{}, false
	}
	return Opt[string]{Set: true, Value: &s}, true
}

func dateString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.DateOnly)
	return &s
}
