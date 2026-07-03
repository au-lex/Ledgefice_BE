package main


import (
	"log"
	"os"

	"github.com/joho/godotenv"

	"github.com/ledgefice/internal/database"
	"github.com/ledgefice/internal/models"
)

func main() {
	// ── Env ───────────────────────────────────────────────────────────────
	if err := godotenv.Load(); err != nil {
		log.Println("⚠️  No .env file found, using system environment variables")
	}

	// ── Database ──────────────────────────────────────────────────────────
	if err := database.Connect(); err != nil {
		log.Fatal("❌ Failed to connect to database:", err)
	}
	defer database.Close()

	if err := database.Migrate(); err != nil {
		log.Fatal("❌ Failed to run migrations:", err)
	}

	// ── Seed admin ────────────────────────────────────────────────────────
	email := os.Getenv("ADMIN_EMAIL")
	password := os.Getenv("ADMIN_PASSWORD")
	firstName := os.Getenv("ADMIN_FIRST_NAME")
	lastName := os.Getenv("ADMIN_LAST_NAME")

	if email == "" {
		email = "admin@ledgefice.com"
	}
	if password == "" {
		password = "Admin@12345"
	}
	if firstName == "" {
		firstName = "Ledgefice"
	}
	if lastName == "" {
		lastName = "Admin"
	}

	// Check if admin already exists — don't create duplicates on re-run.
	var existing models.AdminUser
	if err := database.DB.Where("email = ?", email).First(&existing).Error; err == nil {
		log.Printf("⚠️  Admin already exists with email: %s", email)
		if !existing.IsActive {
			existing.IsActive = true
			database.DB.Save(&existing)
			log.Printf("✅ Reactivated existing admin: %s", email)
		}
		return
	}

	admin := models.AdminUser{
		FirstName: firstName,
		LastName:  lastName,
		Email:     email,
		Password:  password,
		IsActive:  true,
	}

	if err := admin.HashPassword(); err != nil {
		log.Fatal("❌ Failed to hash password:", err)
	}

	if err := database.DB.Create(&admin).Error; err != nil {
		log.Fatal("❌ Failed to create admin user:", err)
	}

	log.Println("✅ Admin user created successfully!")
	log.Printf("   Email:    %s", email)
	log.Printf("   Password: %s", password)
	log.Println("   ⚠️  Change the password after first login!")
}