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
}