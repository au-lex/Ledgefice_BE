package models

import (
	"time"

	"github.com/google/uuid"
)

type SubscriptionStatus string

const (
	SubscriptionStatusPending SubscriptionStatus = "pending"
	SubscriptionStatusPaid    SubscriptionStatus = "paid"
	SubscriptionStatusFailed  SubscriptionStatus = "failed"
)

type MandateStatus string

const (
	MandateStatusNone    MandateStatus = ""
	MandateStatusPending MandateStatus = "pending" // created, awaiting NIBSS token-payment authentication
	MandateStatusActive  MandateStatus = "active"
	MandateStatusFailed  MandateStatus = "failed"
)

type Subscription struct {
	Base
	OrganizationID uuid.UUID          `gorm:"type:uuid;not null;index" json:"organization_id"`
	Organization   *Organization      `gorm:"foreignKey:OrganizationID" json:"organization,omitempty"`
	Plan           PlanType           `gorm:"type:varchar(20);not null" json:"plan"`
	Amount         float64            `gorm:"not null" json:"amount"`
	Currency       string             `gorm:"type:varchar(3);default:'NGN'" json:"currency"`
	OrderReference string             `gorm:"uniqueIndex;not null" json:"order_reference"`
	CheckoutLink   string             `json:"checkout_link"`
	Status         SubscriptionStatus `gorm:"type:varchar(20);default:'pending'" json:"status"`
	TokenKey       string             `json:"token_key,omitempty"` // Nomba tokenized card key, set after first successful payment
	CardType       string             `json:"card_type,omitempty"`
	CardPan        string             `json:"card_pan,omitempty"` // masked, e.g. 234818********7580
	PaidAt         *time.Time         `json:"paid_at"`

	// Direct Debit mandate fields — the fallback renewal path for customers who
	// paid via bank_transfer and have no tokenized card.
	MandateID          string        `json:"mandate_id,omitempty"`
	MandateStatus      MandateStatus `gorm:"type:varchar(20);default:''" json:"mandate_status,omitempty"`
	MandateBankCode    string        `json:"mandate_bank_code,omitempty"`
	MandateAccountLast string        `json:"mandate_account_last,omitempty"` // last 4 digits only, never store full account number

	// Dunning / retry fields — drive the renewal + retry cron. RenewsAt is set
	// on every successful payment (PaidAt + 1 month). When a renewal charge
	// fails, RetryCount/NextRetryAt/DunningStage are updated IN PLACE on this
	// same row rather than creating a new one — a new row is only created once
	// a charge actually succeeds (matching the existing Renew() convention of
	// one row per successful/attempted cycle).
	RenewsAt       *time.Time `json:"renews_at,omitempty"`
	RetryCount     int        `gorm:"default:0" json:"retry_count"`
	NextRetryAt    *time.Time `json:"next_retry_at,omitempty"`
	DunningStage   string     `gorm:"type:varchar(20);default:''" json:"dunning_stage,omitempty"` // "", "retry_1", "retry_2", "retry_3", "cancelled"
	CancelledAt    *time.Time `json:"cancelled_at,omitempty"`
	LastRetryError string     `json:"last_retry_error,omitempty"`
}