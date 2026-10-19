package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/saas"
	"github.com/g0-up/tally/apps/api/internal/sales"
)

func TestRouteModuleNamesThePlanModuleOfEachRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	routes := []struct {
		method, pattern, path, want string
	}{
		{http.MethodPost, "/pos/checkout", "/pos/checkout", saas.ModulePOS},
		{http.MethodGet, "/kits/:id", "/kits/1", saas.ModuleKits},
		{http.MethodPost, "/assembly-orders/:id", "/assembly-orders/1:post", saas.ModuleKits},
		{http.MethodGet, "/lots/:id/genealogy", "/lots/1/genealogy", saas.ModuleKits},
		{http.MethodGet, "/lots/:id", "/lots/1", ""},
		{http.MethodGet, "/campaigns", "/campaigns", saas.ModuleCampaign},
		{http.MethodGet, "/care-tasks", "/care-tasks", saas.ModuleCRM},
		{http.MethodPost, "/analytics/questions", "/analytics/questions", saas.ModuleAIQA},
		{http.MethodGet, "/reports/suggestions/buy", "/reports/suggestions/buy", saas.ModuleReportsAdvanced},
		{http.MethodGet, "/reports/:reportKey", "/reports/season", saas.ModuleReportsAdvanced},
		{http.MethodGet, "/reports/:reportKey/x", "/reports/kits/x", saas.ModuleKits},
		{http.MethodGet, "/reports/:reportKey/y", "/reports/sales_summary/y", ""},
		{http.MethodGet, "/products", "/products", ""},
		{http.MethodGet, "/subscription", "/subscription", ""},
	}
	r := gin.New()
	var got string
	for _, rt := range routes {
		r.Handle(rt.method, APIPrefix+rt.pattern, func(c *gin.Context) { got = routeModule(c) })
	}
	r.GET(AdminAPIPrefix+"/pos", func(c *gin.Context) { got = routeModule(c) })
	for _, rt := range routes {
		got = "unset"
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(rt.method, APIPrefix+rt.path, nil))
		if got != rt.want {
			t.Errorf("%s %s: module = %q, want %q", rt.method, rt.path, got, rt.want)
		}
	}
	got = "unset"
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, AdminAPIPrefix+"/pos", nil))
	if got != "" {
		t.Errorf("a route outside the tenant API has module %q, want none", got)
	}
}

func TestOnboardingListensToTheSalesTopic(t *testing.T) {
	if org.TopicSalePosted != sales.TopicSalePosted {
		t.Fatalf("org listens to %q but sales publishes %q", org.TopicSalePosted, sales.TopicSalePosted)
	}
	if _, ok := org.OnboardingHandlers()[sales.TopicSalePosted]; !ok {
		t.Fatal("no onboarding handler for the sale topic")
	}
}
