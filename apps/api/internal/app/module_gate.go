package app

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/g0-up/tally/apps/api/internal/reporting"
	"github.com/g0-up/tally/apps/api/internal/saas"
)

// moduleRoutes maps the first path segment under /api/v1 to the plan module it belongs to.
var moduleRoutes = map[string]string{
	"pos":                   saas.ModulePOS,
	"kits":                  saas.ModuleKits,
	"assembly-orders":       saas.ModuleKits,
	"disassemblies":         saas.ModuleKits,
	"campaigns":             saas.ModuleCampaign,
	"campaign-attributions": saas.ModuleCampaign,
	"care-tasks":            saas.ModuleCRM,
	"loyalty":               saas.ModuleCRM,
	"analytics":             saas.ModuleAIQA,
}

// advancedReports are the report keys that need the reports_advanced module.
var advancedReports = map[string]string{
	reporting.ReportSeason:       saas.ModuleReportsAdvanced,
	reporting.ReportBuy:          saas.ModuleReportsAdvanced,
	reporting.ReportClearance:    saas.ModuleReportsAdvanced,
	reporting.ReportCashForecast: saas.ModuleReportsAdvanced,
	reporting.ReportKits:         saas.ModuleKits,
}

// routeModule names the plan module of the matched tenant route, "" when the route is open to
// every plan.
func routeModule(c *gin.Context) string {
	path, ok := strings.CutPrefix(c.FullPath(), APIPrefix+"/")
	if !ok {
		return ""
	}
	segment, rest, _ := strings.Cut(path, "/")
	segment, _, _ = strings.Cut(segment, ":")
	switch segment {
	case "lots":
		if strings.HasSuffix(rest, "/genealogy") {
			return saas.ModuleKits
		}
		return ""
	case "reports":
		if strings.HasPrefix(rest, "suggestions/") {
			return saas.ModuleReportsAdvanced
		}
		return advancedReports[c.Param("reportKey")]
	}
	return moduleRoutes[segment]
}
