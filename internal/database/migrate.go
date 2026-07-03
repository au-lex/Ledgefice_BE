package database

import (
	"fmt"
	"log"

	"github.com/ledgefice/internal/models"
)

func Migrate() error {
	log.Println("Running database migrations...")

	err := DB.AutoMigrate(
		&models.Organization{},
		&models.User{},
		&models.Department{},
		&models.VoucherType{},
		&models.CustomField{},
		&models.ApprovalChain{},
		&models.AmountTier{},
		&models.ApproverStep{},
		&models.Voucher{},
		&models.VoucherFieldValue{},
		&models.ApprovalHistory{},
		&models.AuditLog{},
		&models.DuplicateFlag{},
		&models.Subscription{},
		&models.WebhookEvent{},
		&models.PendingSignup{},
		&models.AdminUser{},
	)
	if err != nil {
		log.Printf("Error migrating database: %v", err)
		return fmt.Errorf("failed to migrate database: %w", err)
	}

	log.Println("Database migration completed successfully")
	return nil
}