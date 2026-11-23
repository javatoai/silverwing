package genbuapi

import (
	"sort"
	"strings"
)

type resolvedService struct {
	Name    string `json:"name"`
	AppID   string `json:"app_id"`
	BuiltIn bool   `json:"built_in"`
}

// builtInApplications is a snapshot of the Genbu ANDCN application list.
// Full application names are intentionally matched case-sensitively.
var builtInApplications = map[string]string{
	"mp-pay-center":                         "10010003",
	"mp-siteinfo":                           "10010004",
	"mp-sms-gateway":                        "10010005",
	"mp-tsm":                                "10010006",
	"mp-kds":                                "10010011",
	"mp-kds-front":                          "10010012",
	"mp-mock-unionpay":                      "100100133",
	"mp-ucmp":                               "10010025",
	"mp-kms":                                "10010028",
	"mp-ptaservice":                         "10010032",
	"standard-checkin":                      "10010075",
	"mp-pta-dp-import":                      "10010077",
	"mp-sei-tsm":                            "10010087",
	"mp-transit-notification-service":       "10010089",
	"migration-mp-sei-tsm":                  "10020011",
	"fp-standard-gateway":                   "11010001",
	"fp-xiaomi-gateway":                     "11010002",
	"fp-message-push":                       "11010005",
	"fp-ios-transit-service":                "11010012",
	"fp-android-transit-service":            "11010016",
	"fp-order-center":                       "11010017",
	"fp-android-transit-job":                "11010020",
	"fp-apdu-build-service":                 "11010021",
	"mp-cius-collector":                     "11010022",
	"fp-mini-app-server":                    "11010025",
	"fp-server-gateway":                     "11010027",
	"fp-mobile-gateway":                     "11010028",
	"fp-mobile-gateway-go":                  "11010030",
	"os-mobile-gateway":                     "11010101",
	"os-server-gateway":                     "11010102",
	"os-app-service":                        "11010103",
	"os-transit-service":                    "11010104",
	"os-pay-center-service":                 "11010105",
	"digital-carkey-server":                 "110200028",
	"bp-data-center":                        "11020008",
	"bp-job-manager":                        "11020020",
	"bp-xxl-job":                            "11020021",
	"bp-operation-center":                   "11020023",
	"sentinel":                              "110200239",
	"bp-operate-app":                        "11020024",
	"bp-web-gateway":                        "11020025",
	"mp-mifare-crack-service":               "11020026",
	"marketing-activities-service":          "11020027",
	"bp-api-service":                        "11020028",
	"pta-proxy-service":                     "11020030",
	"bp-data-sync-service":                  "11020055",
	"mp-hsm-gateway":                        "11020091",
	"mp-wulu-hsm-gateway":                   "11020092",
	"os-command-service":                    "11020101",
	"os-card-manager-service":               "11020102",
	"os-strategy-service":                   "11020103",
	"os-key-manager-service":                "11020104",
	"os-trusted-service-manager":            "11020105",
	"os-rta-card-analysis-service":          "11020106",
	"bp-ticket-go":                          "11030029",
	"os-admin-service":                      "11030101",
	"os-admin-job":                          "11030102",
	"snb-transit-ios":                       "11050012",
	"WULU-SDK":                              "11050014",
	"Android-SaaS-SDK":                      "11050015",
	"Android-SaaS-SDK-Support":              "11050016",
	"Alipay-Applet":                         "11050017",
	"WeChat-Applet":                         "11050018",
	"fp-digital-key":                        "12010001",
	"ob-pki-service":                        "12020044",
	"minip-invoice":                         "12050023",
	"fp-transit-h5-portal":                  "12050024",
	"fp-eid-issue":                          "13010001",
	"Smart-AC-SDK":                          "13050001",
	"fp-smart-carkey":                       "15010001",
	"alibabacloud-observability-mcp-server": "16000001",
	"springboot3-demo":                      "824",
	"genbu-test":                            "999999999",
}

// serviceAliases is the single source of truth for service-name groups. The
// map key is the canonical Genbu application name; every name in its value
// list, including the canonical name itself, resolves to that key.
var serviceAliases = map[string][]string{
	// BP services.
	"bp-api-service": {
		"api", "bp-api-service-logstore", "bp-api-service", "bp-api", "api-service",
	},
	"bp-data-center": {
		"dc", "bp-data-center-logstore", "bp-data-center", "data-center",
	},
	"bp-job-manager": {
		"jm", "bp-job-manager-logstore", "bp-job-manager", "job-manager",
	},
	"bp-operation-center": {
		"opc", "bp-operation-center-logstore", "bp-operation-center", "center", "operation-center",
	},
	"bp-web-gateway": {
		"web-gw", "bp-web-gateway-logstore", "bp-web-gateway", "web 网关", "web_gw", "web-gateway",
	},
	"bp-xxl-job": {
		"xxl", "bp-xxl-job-sidecar", "bp-xxl-job", "xj", "xxl-job",
	},

	// FP services.
	"fp-android-transit-job": {
		"atj", "fp-android-transit-job-logstore", "fp-android-transit-job", "android-transit-job",
	},
	"fp-android-transit-service": {
		"ats", "fp-android-transit-service-logstore", "fp-android-transit-service", "fp-android-transit", "android-transit-service", "android-transit",
	},
	"fp-apdu-build-service": {
		"apdu", "fp-apdu-build-service-logstore", "fp-apdu-build-service", "fp-apdu-build", "apdu-build-service", "apdu-build",
	},
	"fp-ios-transit-service": {
		"its", "fp-ios-transit-service-logstore", "fp-ios-transit-service", "fp-ios-transit", "ios-transit-service", "ios-transit",
	},
	"fp-message-push": {
		"mp", "fp-message-push-logstore", "fp-message-push", "message-push",
	},
	"fp-mini-app-server": {
		"mini", "fp-mini-app-server-logstore", "fp-mini-app-server", "mini-app-server",
	},
	"fp-mobile-gateway-go": {
		"fp-mobile-gateway-go", "mobile-gateway-go",
	},
	"fp-mobile-gateway": {
		"mobile-gw", "fp-mobile-gateway-logstore", "fp-mobile-gateway", "mg", "mobile-gateway",
	},
	"fp-order-center": {
		"oc", "fp-order-center-logstore", "fp-order-center", "order-center",
	},
	"fp-sdk-gateway": {
		"fp-sdk-gateway", "fp-sdk-gateway-logstore", "sdk-gateway",
	},
	"fp-server-gateway": {
		"server-gw", "fp-server-gateway-logstore", "fp-server-gateway", "华为网关", "server_gw", "server-gateway",
	},
	"fp-standard-gateway": {
		"std-gw", "fp-standard-gateway-logstore", "fp-standard-gateway", "标准网关", "stagate", "standard-gateway",
	},
	"fp-xiaomi-gateway": {
		"fp-xiaomi-gateway", "fp-xiaomi-gateway-logstore", "xiaomi-gateway",
	},

	// MP services and infrastructure.
	"marketing-activities-service": {
		"market", "marketing-activities-service-log", "marketing-activities-service", "marketing-activities",
	},
	"mp-kms": {
		"kms", "mp-kms-logstore", "mp-kms",
	},
	"mp-pay-center": {
		"pc", "mp-pay-center-logstore", "mp-pay-center", "pay", "pay-center",
	},
	"mp-pta-dp-import": {
		"di", "mp-pta-dp-import-logstore", "mp-pta-dp-import", "dpimport", "pta-dp-import",
	},
	"mp-ptaservice": {
		"pta", "mp-ptaservice-logstore", "mp-ptaservice", "ptaservice",
	},
	"mp-siteinfo": {
		"si", "mp-siteinfo-logstore", "mp-siteinfo", "siteinfo",
	},
	"mp-transit-notification-service": {
		"tns", "mp-transit-notification-service-logstore", "mp-transit-notification-service", "mp-transit-notification", "transit-notification-service", "transit-notification",
	},
	"mp-tsm": {
		"tsm", "mp-tsm-logstore", "mp-tsm",
	},
	"mp-ucmp": {
		"ucmp", "mp-ucmp-logstore", "mp-ucmp",
	},
	"pta-proxy-service": {
		"pps", "pta-proxy-service", "pta-proxy",
	},
	"bp-data-sync-service": {
		"dss", "data-sync-service-sidecar", "data-sync-service", "bp-data-sync-service", "data-sync",
	},
	"alibabacloud-observability-mcp-server": {
		"sls", "alibabacloud-observability-mcp-server",
	},

	// Existing compatibility aliases outside the attachment.
	"bp-operate-app": {
		"app", "bp-operate-app", "bp-operation-app", "operation-app",
	},
	"mp-mifare-crack-service": {
		"mcs", "mp-mifare-crack-service",
	},
}

var serviceAliasIndex = buildServiceAliasIndex(serviceAliases)

var (
	BuiltInApplications = builtInApplications
	ServiceAliases      = serviceAliases
)

func buildServiceAliasIndex(groups map[string][]string) map[string]string {
	index := make(map[string]string)
	for canonical, names := range groups {
		for _, name := range names {
			key := strings.ToLower(strings.TrimSpace(name))
			if previous, exists := index[key]; exists && previous != canonical {
				panic("service alias " + name + " is assigned to multiple canonical services")
			}
			index[key] = canonical
		}
	}
	return index
}

func resolveService(input string) resolvedService {
	name := input
	if canonical, ok := serviceAliasIndex[strings.ToLower(strings.TrimSpace(input))]; ok {
		name = canonical
	}
	appID, ok := builtInApplications[name]
	return resolvedService{Name: name, AppID: appID, BuiltIn: ok}
}

// Service identifies a logical Genbu application without credentials.
type Service = resolvedService

// ResolveService maps a service name or alias to its logical Genbu identity.
func ResolveService(input string) Service { return resolveService(input) }

// ListServices returns the built-in logical Genbu service identifiers.
func ListServices() []Service {
	services := make([]Service, 0, len(builtInApplications))
	for name, appID := range builtInApplications {
		services = append(services, Service{Name: name, AppID: appID, BuiltIn: true})
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	return services
}

// ListEnvironments returns the logical environment identifiers accepted by
// Genbu's service configuration and pod APIs.
func ListEnvironments() []string { return []string{"uat", "prod"} }
