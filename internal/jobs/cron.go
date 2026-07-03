package jobs


import (
	"log"
	"time"

	"github.com/ledgefice/internal/services"
)

func StartRenewalCron(svc *services.RenewalService, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {

		runOnce(svc)
		for range ticker.C {
			runOnce(svc)
		}
	}()
}

func runOnce(svc *services.RenewalService) {
	log.Println("🔁 renewal cron: checking for due subscriptions")
	if err := svc.ProcessDueRenewals(); err != nil {
		log.Println("❌ renewal cron error:", err)
		return
	}
	log.Println("✅ renewal cron: pass complete")
}