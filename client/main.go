package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/Kaese72/authentication/usertoken"
	"github.com/Kaese72/cloud-connect/client/internal/applianceregistry"
	"github.com/Kaese72/cloud-connect/client/internal/cloudconnectwebapp"
	"github.com/Kaese72/cloud-connect/client/internal/cloudloginwebapp"
	"github.com/Kaese72/cloud-connect/client/internal/config"
	"github.com/Kaese72/cloud-connect/client/internal/logging"
	"github.com/Kaese72/cloud-connect/client/internal/nginxboot"
	"github.com/Kaese72/cloud-connect/client/internal/persistence/mariadb"
	"github.com/Kaese72/cloud-connect/client/internal/tunnel"
	"github.com/Kaese72/huemie-lib/k8sauth"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humamux"
	"github.com/gorilla/mux"

	_ "go.elastic.co/apm/module/apmsql/mysql"
)

// internalTrustedServiceAccountNames are the only Kubernetes ServiceAccounts
// allowed to call the internal cloud-login listener -- see
// k8sauth.RequireServiceAccount. Hardcoded, not configuration: who may call
// in on the appliance's behalf of the cloud is a security boundary of this
// service, not a per-deployment knob. Mirrors authentication's own
// internalTrustedServiceAccountNames.
var internalTrustedServiceAccountNames = []string{"authentication"}

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

	pubKey, err := usertoken.LoadPublicKeyFromFile(config.Loaded.Auth.UseTokenRSAPublicKeyPath)
	if err != nil {
		logging.Error(err.Error(), context.Background())
		os.Exit(1)
	}

	registryClient := applianceregistry.NewClient(config.Loaded.ApplianceRegistry.BaseURL)
	supervisor := tunnel.NewSupervisor()
	app := cloudconnectwebapp.NewWebApp(dbPersistence, registryClient, supervisor, config.Loaded.Cloud.EnrollBaseURL)
	cloudLoginApp := cloudloginwebapp.NewWebApp(dbPersistence, registryClient, config.Loaded.Cloud.LoginBaseURL)

	if err := app.ResumeIfEnrolled(context.Background()); err != nil {
		logging.Error(err.Error(), context.Background())
		os.Exit(1)
	}

	router := mux.NewRouter()
	router.Use(usertoken.Middleware(pubKey, "/cloud-connect-client/openapi", "/cloud-connect-client/docs"))
	humaConfig := huma.DefaultConfig("cloud-connect-client", "1.0.0")
	humaConfig.OpenAPIPath = "/cloud-connect-client/openapi"
	humaConfig.DocsPath = "/cloud-connect-client/docs"
	api := humamux.New(router, humaConfig)

	huma.Get(api, "/cloud-connect-client/v0/status", app.GetStatus)
	huma.Post(api, "/cloud-connect-client/v0/enrollment/start", app.StartEnrollment)
	huma.Post(api, "/cloud-connect-client/v0/enrollment/complete", app.CompleteEnrollment)
	huma.Post(api, "/cloud-connect-client/v0/enrollment/reset", app.ResetEnrollment)
	huma.Post(api, "/cloud-connect-client/v0/enrollment/rotate-secret", app.RotateSecret)

	clientset, err := k8sauth.NewInClusterClientset(config.Loaded.Kubernetes.ApiserverProxyURL)
	if err != nil {
		logging.Error("failed to build in-cluster Kubernetes clientset: "+err.Error(), context.TODO())
		os.Exit(1)
	}

	// debug.namespace is a local-development-only override (see
	// config.DebugConfig): outside a real pod there is no
	// /var/run/secrets/kubernetes.io/serviceaccount/namespace file for
	// k8sauth.CurrentNamespace to read, so it must be supplied explicitly
	// instead. Never set in a real deployment, where the real namespace is
	// always used.
	namespace := config.Loaded.Debug.Namespace
	if namespace == "" {
		namespace, err = k8sauth.CurrentNamespace()
		if err != nil {
			logging.Error("failed to determine this pod's own namespace: "+err.Error(), context.TODO())
			os.Exit(1)
		}
	}
	requireServiceAccount := k8sauth.RequireServiceAccount(clientset, internalTrustedServiceAccountNames, config.Loaded.Internal.TokenAudience, namespace)

	// Internal-only cloud-login API for the authentication service, on its own
	// port so the ingress can never route to it.
	internalRouter := mux.NewRouter()
	internalRouter.Use(requireServiceAccount)
	internalHumaConfig := huma.DefaultConfig("cloud-connect-client-internal", "1.0.0")
	internalHumaConfig.OpenAPIPath = "/cloud-connect-client/internal/openapi"
	internalHumaConfig.DocsPath = ""
	internalAPI := humamux.New(internalRouter, internalHumaConfig)
	huma.Get(internalAPI, "/cloud-connect-client/v0/internal/cloud-login/status", cloudLoginApp.GetStatus)
	huma.Post(internalAPI, "/cloud-connect-client/v0/internal/cloud-login/start", cloudLoginApp.Start)
	huma.Post(internalAPI, "/cloud-connect-client/v0/internal/cloud-login/redeem", cloudLoginApp.Redeem)
	huma.Get(internalAPI, "/cloud-connect-client/v0/internal/cloud-login/access/{cloudUserId:[0-9]+}", cloudLoginApp.CheckAccess)
	go func() {
		if err := http.ListenAndServe(fmt.Sprintf(":%d", config.Loaded.InternalPort), internalRouter); err != nil {
			logging.Error(err.Error(), context.TODO())
			os.Exit(1)
		}
	}()

	if err := http.ListenAndServe(fmt.Sprintf(":%d", config.Loaded.Port), router); err != nil {
		logging.Error(err.Error(), context.TODO())
	}
}
