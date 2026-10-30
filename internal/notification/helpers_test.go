package notification

import (
	"context"
	"testing"
	"time"
)

func readRecords(t *testing.T, service *Service) ([]Record, error) {
	t.Helper()
	records, _, err := service.RecordPage(t.Context(), 30, 0)
	return records, err
}
func shutdownService(t *testing.T, service *Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
	defer cancel()
	if err := service.Shutdown(ctx); err != nil {
		t.Errorf("shutdown notification service: %v", err)
	}
}
