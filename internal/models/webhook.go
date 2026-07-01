package models

type WebhookEvent struct {
	Base
	RequestID string `gorm:"uniqueIndex;not null" json:"request_id"`
	EventType string `gorm:"type:varchar(50)" json:"event_type"`
}