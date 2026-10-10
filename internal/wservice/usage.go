package wservice

import (
	"github.com/vivek9102/opencode-odometer/internal/app"
	"path/filepath"
)

func (s *Service) UsageReport(start, end string) (app.UsageReport, error) {
	return s.App.UsageReport(start, end)
}
func (s *Service) ExportUsageCsv(start, end string) (string, error) {
	return s.App.ExportUsageCSV(filepath.Join(dataDir(), "exports"), start, end)
}
