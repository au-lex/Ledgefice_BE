package services

import (
	"github.com/google/uuid"
	"github.com/ledgefice/internal/database"
	"github.com/ledgefice/internal/models"
)

type AuditInput struct {
	OrganizationID *uuid.UUID
	ActorID        *uuid.UUID
	ActorEmail     string
	ActorName      string
	Action         models.AuditAction
	Module         models.AuditModule
	ResourceID     string
	Description    string
	IPAddress      string
	UserAgent      string
}

func WriteAudit(input AuditInput) {
	log := models.AuditLog{
		OrganizationID: input.OrganizationID,
		ActorID:        input.ActorID,
		ActorEmail:     input.ActorEmail,
		ActorName:      input.ActorName,
		Action:         input.Action,
		Module:         input.Module,
		ResourceID:     input.ResourceID,
		Description:    input.Description,
		IPAddress:      input.IPAddress,
		UserAgent:      input.UserAgent,
	}
	// Fire-and-forget — don't block the request
	go database.DB.Create(&log)
}