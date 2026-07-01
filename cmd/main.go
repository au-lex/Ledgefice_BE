package main

import (
	"log"
	"os"

	"github.com/cloudinary/cloudinary-go/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/ledgefice/internal/config"
	"github.com/ledgefice/internal/database"
	"github.com/ledgefice/internal/middleware"
	"github.com/ledgefice/internal/routes"
	"github.com/ledgefice/internal/services"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}
	// ── Database ──────────────────────────────────────────────────────────
	if err := database.Connect(); err != nil {
		log.Fatal("❌ Failed to connect to database:", err)
	}
	defer database.Close()

	if err := database.Migrate(); err != nil {
		log.Fatal("❌ Failed to run migrations:", err)
	}
	log.Println("✅ Database connected and migrated")

		// ── Cloudinary ────────────────────────────────────────────────────────
	cld, err := cloudinary.NewFromParams(
		os.Getenv("CLOUDINARY_CLOUD_NAME"),
		os.Getenv("CLOUDINARY_API_KEY"),
		os.Getenv("CLOUDINARY_API_SECRET"),
	)
	if err != nil {
		log.Fatal("❌ Failed to initialise Cloudinary:", err)
	}
		emailSvc := services.NewEmailClient()
	imgSvc := services.NewImageService(cld)
	log.Println("✅ Cloudinary ready")

	app := fiber.New(fiber.Config{
		AppName: "VMS API v1",
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return c.Status(code).JSON(fiber.Map{"error": err.Error()})
		},
	})

	middleware.Register(app)

	routes.RegisterRoutes(app, cfg.JWTSecret, imgSvc,emailSvc)

	log.Printf("🚀 VMS API listening on :%s [%s]", cfg.AppPort, cfg.AppEnv)
	if err := app.Listen(":" + cfg.AppPort); err != nil {
		log.Fatalf("server error: %v", err)
	}
}