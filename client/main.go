package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/Kaese72/cloud-connect/client/internal/applianceregistry"
	"github.com/Kaese72/cloud-connect/client/internal/cloudconnectwebapp"
	"github.com/Kaese72/cloud-connect/client/internal/config"
	"github.com/Kaese72/cloud-connect/client/internal/logging"
	"github.com/Kaese72/cloud-connect/client/internal/nginxboot"
	"github.com/Kaese72/cloud-connect/client/internal/persistence/mariadb"
	"github.com/Kaese72/cloud-connect/client/internal/tunnel"
	"github.com/Kaese72/huemie-lib/middleware"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humamux"
	"github.com/gorilla/mux"

	_ "go.elastic.co/apm/module/apmsql/mysql"
)

func main() {
	if err := config.Loaded.Validate(); err != nil {
		logging.Error(err.Error(), context.TODO())
		os.Exit(1)
	}

	if err := nginxboot.Render(config.Loaded.Tunnel.AllowedHostPattern, config.Loaded.Tunnel.LocalIngress); err != nil {
		logging.Error("failed to render nginx config: "+err.Error(), context.Background())
		os.Exit(1)
	}
	if err := nginxboot.Start(); err != nil {
		logging.Error("failed to start nginx: "+err.Error(), context.Background())
		os.Exit(1)
	}

	dbPersistence, err := mariadb.NewMariadbPersistence(config.Loaded.Database)
	if err != nil {
		logging.Error(err.Error(), context.Background())
		os.Exit(1)
	}

	pubKey, err := middleware.LoadPublicKeyFromFile(config.Loaded.Auth.UseTokenRSAPublicKeyPath)
	if err != nil {
		logging.Error(err.Error(), context.Background())
		os.Exit(1)
	}

	registryClient := applianceregistry.NewClient(config.Loaded.ApplianceRegistry.BaseURL)
	supervisor := tunnel.NewSupervisor()
	app := cloudconnectwebapp.NewWebApp(dbPersistence, registryClient, supervisor, config.Loaded.Cloud.EnrollBaseURL)

	if err := app.ResumeIfEnrolled(context.Background()); err != nil {
		logging.Error(err.Error(), context.Background())
		os.Exit(1)
	}

	router := mux.NewRouter()
	router.Use(middleware.UseTokenMiddleware(pubKey, "/cloud-connect-client/openapi", "/cloud-connect-client/docs"))
	humaConfig := huma.DefaultConfig("cloud-connect-client", "1.0.0")
	humaConfig.OpenAPIPath = "/cloud-connect-client/openapi"
	humaConfig.DocsPath = "/cloud-connect-client/docs"
	api := humamux.New(router, humaConfig)

	huma.Get(api, "/cloud-connect-client/v0/status", app.GetStatus)
	huma.Post(api, "/cloud-connect-client/v0/enrollment/start", app.StartEnrollment)
	huma.Post(api, "/cloud-connect-client/v0/enrollment/complete", app.CompleteEnrollment)
	huma.Post(api, "/cloud-connect-client/v0/enrollment/reset", app.ResetEnrollment)

	if err := http.ListenAndServe(fmt.Sprintf(":%d", config.Loaded.Port), router); err != nil {
		logging.Error(err.Error(), context.TODO())
	}
}
