package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	z "github.com/Oudwins/zog"
	"github.com/rs/xid"
)

var CreateBinaryAppRequestSchema = z.Struct(z.Shape{
	"teamSlug": z.String().
		Required(z.Message("Team Slug must be provided")).
		Min(3).
		Max(50, z.Message("Team Slug must be between 3 and 50 characters")),
	"deploymentId": z.String().
		Required(z.Message("DeploymentID must be provided")).
		Max(100, z.Message("DeploymentID must be between 1 and 100 characters")),
	"appId": z.String().
		Required(z.Message("App ID must be provided")).
		Min(3).
		Max(100, z.Message("App ID must be between 3 and 100 characters")),
	"appSlug": z.String().
		Required(z.Message("App Slug must be provided")).
		Min(3).
		Max(50, z.Message("App Slug must be between 3 and 50 characters")),
	"artifactName": z.String().
		Required(z.Message("Artifact name must be provided")).
		Min(1).
		Max(200, z.Message("Artifact name must be between 1 and 200 characters")),
	"artifactSource": z.String().
		Required(z.Message("Artifact source must be provided")).
		Min(1).
		Max(2000, z.Message("Artifact source must be between 1 and 2000 characters")),
	"artifactVersion": z.String().
		Required(z.Message("Artifact version must be provided")).
		Min(1).
		Max(100, z.Message("Artifact version must be between 1 and 100 characters")),
	"environmentName": z.String().
		Required(z.Message("Environment name must be provided")).
		Min(1).
		Max(100, z.Message("Environment name must be between 1 and 100 characters")),
	"callbackUrl": z.String().
		Required(z.Message("CallbackURL must be provided")).
		URL().
		Max(2000, z.Message("CallbackURL must be between 1 and 2000 characters")),
	"domain": z.String().
		Optional().
		Max(253, z.Message("Domain must be between 1 and 253 characters")),
	"port": z.Int().Required(z.Message("Port must be provided")).
		GT(0).
		LT(65536).
		Not().OneOf([]int{80, 443, 9640}, z.Message("Ports: 80, 443, 9640 is reserved")),
})

const (
	CreateBinaryAppAction = "create_binary_app"
	DeployBinaryAppAction = "deploy_binary_app"
)

var DeployBinaryAppRequestSchema = z.Struct(z.Shape{
	"teamSlug": z.String().
		Required(z.Message("Team Slug must be provided")).
		Min(3).
		Max(50, z.Message("Team Slug must be between 3 and 50 characters")),
	"deploymentId": z.String().
		Required(z.Message("DeploymentID must be provided")).
		Max(100, z.Message("DeploymentID must be between 1 and 100 characters")),
	"appId": z.String().
		Required(z.Message("App ID must be provided")).
		Min(3).
		Max(100, z.Message("App ID must be between 3 and 100 characters")),
	"appSlug": z.String().
		Required(z.Message("App Slug must be provided")).
		Min(3).
		Max(50, z.Message("App Slug must be between 3 and 50 characters")),
	"artifactName": z.String().
		Required(z.Message("Artifact name must be provided")).
		Min(1).
		Max(200, z.Message("Artifact name must be between 1 and 200 characters")),
	"artifactSource": z.String().
		Required(z.Message("Artifact source must be provided")).
		Min(1).
		Max(2000, z.Message("Artifact source must be between 1 and 2000 characters")),
	"artifactVersion": z.String().
		Required(z.Message("Artifact version must be provided")).
		Min(1).
		Max(100, z.Message("Artifact version must be between 1 and 100 characters")),
	"environmentName": z.String().
		Required(z.Message("Environment name must be provided")).
		Min(1).
		Max(100, z.Message("Environment name must be between 1 and 100 characters")),
	"callbackUrl": z.String().
		Required(z.Message("CallbackURL must be provided")).
		URL().
		Max(2000, z.Message("CallbackURL must be between 1 and 2000 characters")),
})

// CreateBinaryApp implements ServerInterface.
func (h *APIHandler) CreateBinaryApp(w http.ResponseWriter, r *http.Request) {
	var req CreateBinaryAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDeployResponse(
			w,
			http.StatusBadRequest,
			"error",
			fmt.Sprintf("invalid request body: %v", err),
			"",
		)

		return
	}
	if validationErrors := CreateBinaryAppRequestSchema.Validate(&req); validationErrors != nil {
		// handle errors -> see Errors section
		var validationErrorMessages []string
		for _, ve := range validationErrors {
			validationErrorMessages = append(validationErrorMessages, ve.Message)
		}

		writeDeployResponse(
			w,
			http.StatusBadRequest,
			"error",
			fmt.Sprintf("validation errors: %s", strings.Join(validationErrorMessages, "; ")),
			"",
		)
		return
	}

	writeDeployResponse(
		w,
		http.StatusAccepted,
		"accepted",
		"app creation accepted",
		"",
	)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		groupingID := xid.New().String()

		emitter := NewCallbackEmitter(req.CallbackUrl, h.apiKey)

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			GroupingID: groupingID,
			Action:     CreateBinaryAppAction,
			Scope:      "action",
			Status:     "in_progress",
			Message:    fmt.Sprintf("Starting deployment for %s/%s", req.AppSlug, req.EnvironmentName),
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			GroupingID: groupingID,
			Action:     CreateBinaryAppAction,
			Step:       "create_app_directory",
			Scope:      "step",
			Status:     "in_progress",
			Message:    fmt.Sprintf("Creating binary app %s/%s", req.AppSlug, req.EnvironmentName),
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}

		// Create app directory (under /opt/mithlond/apps - group-writable, no sudo needed)
		// e.g., /opt/mithlond/apps/team_slug-team_id/environment
		appDir := path.Join(
			appsBaseDir(),
			strings.ToLower(req.TeamSlug)+"-"+strings.ToLower(req.AppId),
			strings.ToLower(req.EnvironmentName),
		)
		if err := os.MkdirAll(appDir, 0o755); err != nil {
			if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
				Scope:      "step",
				GroupingID: groupingID,
				Action:     CreateBinaryAppAction,
				Step:       "create_app_directory",
				Status:     "failed",
				Message:    "Failed to create app directory",
				Error:      fmt.Sprintf("failed to create app directory: %v", err),
			}); err != nil {
				slog.Error("failed to emit deployment event", "error", err)
			}
			return
		}

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			Scope:      "step",
			GroupingID: groupingID,
			Action:     CreateBinaryAppAction,
			Step:       "create_app_directory",
			Status:     "completed",
			Message:    "App directory created successfully",
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			Scope:      "step",
			GroupingID: groupingID,
			Action:     CreateBinaryAppAction,
			Step:       "create_config_directory",
			Status:     "in_progress",
			Message:    "Creating config directory",
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}

		// e.g., /etc/mithlond/apps/team_slug-team_id/environment
		configDir := path.Join(
			appsConfigDir(),
			strings.ToLower(req.TeamSlug)+"-"+strings.ToLower(req.AppId),
			strings.ToLower(req.EnvironmentName),
		)
		if err := sudoMkdirAll(configDir); err != nil {
			if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
				Scope:      "step",
				GroupingID: groupingID,
				Action:     CreateBinaryAppAction,
				Step:       "create_config_directory",
				Status:     "failed",
				Message:    "Failed to create config directory",
				Error:      fmt.Sprintf("failed to create config directory: %v", err),
			}); err != nil {
				slog.Error("failed to emit deployment event", "error", err)
			}
			return
		}

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			Scope:      "step",
			GroupingID: groupingID,
			Action:     CreateBinaryAppAction,
			Step:       "create_config_directory",
			Status:     "completed",
			Message:    "Config directory created successfully",
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			Scope:      "step",
			GroupingID: groupingID,
			Action:     CreateBinaryAppAction,
			Step:       "download",
			Status:     "in_progress",
			Message:    "Starting binary download",
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}

		// Download binary
		binaryPath := path.Join(appDir, req.ArtifactVersion)
		binaryURL, _, err := buildArtifactURLs(
			req.ArtifactSource,
			req.ArtifactVersion,
			req.ArtifactName,
		)
		if err != nil {
			if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
				Scope:      "step",
				GroupingID: groupingID,
				Action:     CreateBinaryAppAction,
				Step:       "download",
				Status:     "failed",
				Message:    "Failed to build artifact URLs",
				Error:      fmt.Sprintf("failed to build artifact URLs: %v", err),
			}); err != nil {
				slog.Error("failed to emit deployment event", "error", err)
			}
			return
		}

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			Scope:      "step",
			GroupingID: groupingID,
			Action:     CreateBinaryAppAction,
			Step:       "download",
			Status:     "in_progress",
			Message:    fmt.Sprintf("Downloading binary version %s", req.ArtifactVersion),
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}

		if err := downloadToFile(ctx, binaryURL, binaryPath); err != nil {
			if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
				Scope:      "step",
				GroupingID: groupingID,
				Action:     CreateBinaryAppAction,
				Step:       "download",
				Status:     "failed",
				Message:    "Failed to download binary",
				Error:      fmt.Sprintf("failed to download binary: %v", err),
			}); err != nil {
				slog.Error("failed to emit deployment event", "error", err)
			}
			return
		}

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			Scope:      "step",
			Action:     CreateBinaryAppAction,
			GroupingID: groupingID,
			Step:       "download",
			Status:     "completed",
			Message:    "Binary downloaded successfully",
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}

		// Make binary executable
		if err := os.Chmod(binaryPath, 0o755); err != nil {
			if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
				Scope:      "step",
				GroupingID: groupingID,
				Action:     CreateBinaryAppAction,
				Step:       "switch",
				Status:     "failed",
				Message:    "Failed to chmod binary",
				Error:      fmt.Sprintf("failed to chmod binary: %v", err),
			}); err != nil {
				slog.Error("failed to emit deployment event", "error", err)
			}
			return
		}

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			Scope:      "step",
			GroupingID: groupingID,
			Action:     CreateBinaryAppAction,
			Step:       "switch",
			Status:     "completed",
			Message:    "Binary chmodded successfully",
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			Scope:      "step",
			GroupingID: groupingID,
			Action:     CreateBinaryAppAction,
			Step:       "environmental_variables",
			Status:     "in_progress",
			Message:    "Writing environment variables",
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}

		// Write env file to config directory (requires sudo)
		if req.EnvVars != nil && len(*req.EnvVars) > 0 {
			envPath := path.Join(configDir, "env")
			var envContent strings.Builder
			for key, value := range *req.EnvVars {
				fmt.Fprintf(&envContent, "%s=%s\n", key, value)
			}

			if err := sudoWriteFile(envPath, []byte(envContent.String()), 0o640); err != nil {
				if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
					Scope:      "step",
					GroupingID: groupingID,
					Action:     CreateBinaryAppAction,
					Step:       "environmental_variables",
					Status:     "failed",
					Message:    "Failed to write env file",
					Error:      fmt.Sprintf("failed to write env file: %v", err),
				}); err != nil {
					slog.Error("failed to emit deployment event", "error", err)
				}
				return
			}

		}

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			Scope:      "step",
			GroupingID: groupingID,
			Action:     CreateBinaryAppAction,
			Step:       "environmental_variables",
			Status:     "completed",
			Message:    "Environment variables configured",
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			Scope:      "step",
			GroupingID: groupingID,
			Action:     CreateBinaryAppAction,
			Step:       "systemd_service",
			Status:     "in_progress",
			Message:    "Creating systemd service",
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}

		// Create systemd service (system unit, requires sudo)
		serviceName := strings.ToLower(
			fmt.Sprintf("%s--%s--%s", req.TeamSlug, req.AppSlug, req.EnvironmentName),
		)
		if err := createSystemdService(serviceName, binaryPath, appDir, configDir, req.Port, nil); err != nil {
			if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
				Scope:      "step",
				GroupingID: groupingID,
				Action:     CreateBinaryAppAction,
				Step:       "systemd_service",
				Status:     "failed",
				Message:    "Failed to create systemd service",
				Error:      fmt.Sprintf("failed to create systemd service: %v", err),
			}); err != nil {
				slog.Error("failed to emit deployment event", "error", err)
			}
			return
		}

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			Scope:      "step",
			Action:     CreateBinaryAppAction,
			GroupingID: groupingID,
			Step:       "systemd_service",
			Status:     "completed",
			Message:    "Systemd service created successfully",
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			Scope:      "step",
			GroupingID: groupingID,
			Action:     CreateBinaryAppAction,
			Step:       "start_service",
			Status:     "in_progress",
			Message:    "Starting service",
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}

		// Enable and start service (requires sudo for system units)
		if err := sudoRun("systemctl", "daemon-reload").Run(); err != nil {
			if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
				Scope:      "step",
				Action:     CreateBinaryAppAction,
				GroupingID: groupingID,
				Step:       "start_service",
				Status:     "failed",
				Message:    "Failed to reload systemd",
				Error:      fmt.Sprintf("failed to reload systemd: %v", err),
			}); err != nil {
				slog.Error("failed to emit deployment event", "error", err)
			}
			return
		}

		if err := sudoRun("systemctl", "enable", serviceName).Run(); err != nil {
			if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
				Scope:      "step",
				Action:     CreateBinaryAppAction,
				GroupingID: groupingID,
				Step:       "start_service",
				Status:     "failed",
				Message:    "Failed to enable service",
				Error:      fmt.Sprintf("failed to enable service: %v", err),
			}); err != nil {
				slog.Error("failed to emit deployment event", "error", err)
			}
			return
		}

		if err := sudoRun("systemctl", "start", serviceName).Run(); err != nil {
			if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
				Scope:      "step",
				GroupingID: groupingID,
				Action:     CreateBinaryAppAction,
				Step:       "start_service",
				Status:     "failed",
				Message:    "Failed to start service",
				Error:      fmt.Sprintf("failed to start service: %v", err),
			}); err != nil {
				slog.Error("failed to emit deployment event", "error", err)
			}
			return
		}

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			GroupingID: groupingID,
			Scope:      "step",
			Action:     CreateBinaryAppAction,
			Step:       "start_service",
			Status:     "completed",
			Message:    "Service started successfully",
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			Action:     CreateBinaryAppAction,
			GroupingID: groupingID,
			Step:       "caddy_configuration",
			Scope:      "step",
			Status:     "in_progress",
			Message:    "Configuring Caddy route",
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}

		caddyManager := NewCaddyManager()
		if err := caddyManager.ConfigureRoute(req.Domain, req.Port); err != nil {
			if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
				Action:     CreateBinaryAppAction,
				GroupingID: groupingID,
				Scope:      "step",
				Step:       "caddy_configuration",
				Status:     "failed",
				Message:    "Failed to configure Caddy route",
				Error:      fmt.Sprintf("failed to configure Caddy route: %v", err),
			}); err != nil {
				slog.Error("failed to emit deployment event", "error", err)
			}
			return
		}

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			GroupingID: groupingID,
			Action:     CreateBinaryAppAction,
			Scope:      "step",
			Step:       "caddy_configuration",
			Status:     "completed",
			Message:    "Caddy route configured successfully",
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}

		if err := emitter.EmitDeploymentEvent(ctx, DeploymentEvent{
			GroupingID: groupingID,
			Action:     CreateBinaryAppAction,
			Scope:      "action",
			Status:     "completed",
			Message:    "Deployment completed successfully",
		}); err != nil {
			slog.Error("failed to emit deployment event", "error", err)
			return
		}
	}()
}

// DeployBinaryApp implements ServerInterface.
func (h *APIHandler) DeployBinaryApp(w http.ResponseWriter, r *http.Request) {
	var req DeployBinaryAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDeployResponse(
			w,
			http.StatusBadRequest,
			"error",
			fmt.Sprintf("invalid request body: %v", err),
			"",
		)
		return
	}
	if validationErrors := DeployBinaryAppRequestSchema.Validate(&req); validationErrors != nil {
		var msgs []string
		for _, ve := range validationErrors {
			msgs = append(msgs, ve.Message)
		}
		writeDeployResponse(
			w,
			http.StatusBadRequest,
			"error",
			fmt.Sprintf("validation errors: %s", strings.Join(msgs, "; ")),
			"",
		)
		return
	}

	writeDeployResponse(w, http.StatusAccepted, "accepted", "app deployment accepted", "")

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		groupingID := xid.New().String()
		emitter := NewCallbackEmitter(req.CallbackUrl, h.apiKey)

		emit := func(ev DeploymentEvent) bool {
			ev.GroupingID = groupingID
			ev.Action = DeployBinaryAppAction
			if err := emitter.EmitDeploymentEvent(ctx, ev); err != nil {
				slog.Error("failed to emit deployment event", "error", err)
				return false
			}
			return true
		}

		if !emit(DeploymentEvent{
			Scope:   "action",
			Status:  "in_progress",
			Message: fmt.Sprintf("Starting deployment for %s/%s", req.AppSlug, req.EnvironmentName),
		}) {
			return
		}

		appDir := path.Join(
			appsBaseDir(),
			strings.ToLower(req.TeamSlug)+"-"+strings.ToLower(req.AppId),
			strings.ToLower(req.EnvironmentName),
		)
		configDir := path.Join(
			appsConfigDir(),
			strings.ToLower(req.TeamSlug)+"-"+strings.ToLower(req.AppId),
			strings.ToLower(req.EnvironmentName),
		)

		if _, err := os.Stat(appDir); os.IsNotExist(err) {
			emit(DeploymentEvent{
				Scope:   "step",
				Step:    "download",
				Status:  "failed",
				Message: "App does not exist, use create endpoint first",
				Error:   err.Error(),
			})
			return
		}

		if !emit(DeploymentEvent{
			Scope:   "step",
			Step:    "download",
			Status:  "in_progress",
			Message: fmt.Sprintf("Downloading binary version %s", req.ArtifactVersion),
		}) {
			return
		}

		binaryPath := path.Join(appDir, req.ArtifactVersion)
		binaryURL, _, err := buildArtifactURLs(req.ArtifactSource, req.ArtifactVersion, req.ArtifactName)
		if err != nil {
			emit(DeploymentEvent{
				Scope:   "step",
				Step:    "download",
				Status:  "failed",
				Message: "Failed to build artifact URLs",
				Error:   err.Error(),
			})
			return
		}

		if err := downloadToFile(ctx, binaryURL, binaryPath); err != nil {
			emit(DeploymentEvent{
				Scope:   "step",
				Step:    "download",
				Status:  "failed",
				Message: "Failed to download binary",
				Error:   err.Error(),
			})
			return
		}

		if err := os.Chmod(binaryPath, 0o755); err != nil {
			emit(DeploymentEvent{
				Scope:   "step",
				Step:    "download",
				Status:  "failed",
				Message: "Failed to chmod binary",
				Error:   err.Error(),
			})
			return
		}

		if !emit(DeploymentEvent{
			Scope:   "step",
			Step:    "download",
			Status:  "completed",
			Message: "Binary downloaded successfully",
		}) {
			return
		}

		if req.EnvVars != nil && len(*req.EnvVars) > 0 {
			envPath := path.Join(configDir, "env")
			var envContent strings.Builder
			for key, value := range *req.EnvVars {
				fmt.Fprintf(&envContent, "%s=%s\n", key, value)
			}
			if err := sudoWriteFile(envPath, []byte(envContent.String()), 0o640); err != nil {
				emit(DeploymentEvent{
					Scope:   "step",
					Step:    "environmental_variables",
					Status:  "failed",
					Message: "Failed to write env file",
					Error:   err.Error(),
				})
				return
			}
			emit(DeploymentEvent{
				Scope:   "step",
				Step:    "environmental_variables",
				Status:  "completed",
				Message: "Environment variables updated",
			})
		}

		serviceName := strings.ToLower(
			fmt.Sprintf("%s--%s--%s", req.TeamSlug, req.AppSlug, req.EnvironmentName),
		)

		if !emit(DeploymentEvent{
			Scope:   "step",
			Step:    "switch",
			Status:  "in_progress",
			Message: "Updating systemd service",
		}) {
			return
		}

		servicePath := systemdServicePath(serviceName)
		serviceContent, err := os.ReadFile(servicePath)
		if err != nil {
			emit(DeploymentEvent{
				Scope:   "step",
				Step:    "switch",
				Status:  "failed",
				Message: "Failed to read current service file",
				Error:   err.Error(),
			})
			return
		}

		port := 0
		for _, line := range strings.Split(string(serviceContent), "\n") {
			if strings.Contains(line, "Environment=PORT=") {
				parts := strings.SplitN(line, "=", 3)
				if len(parts) == 3 {
					_, _ = fmt.Sscanf(strings.TrimSpace(parts[2]), "%d", &port)
				}
			}
		}

		if err := createSystemdService(serviceName, binaryPath, appDir, configDir, port, req.Args); err != nil {
			emit(DeploymentEvent{
				Scope:   "step",
				Step:    "switch",
				Status:  "failed",
				Message: "Failed to update systemd service",
				Error:   err.Error(),
			})
			return
		}

		emit(DeploymentEvent{
			Scope:   "step",
			Step:    "switch",
			Status:  "completed",
			Message: "Systemd service updated",
		})

		if !emit(DeploymentEvent{
			Scope:   "step",
			Step:    "restart_service",
			Status:  "in_progress",
			Message: "Restarting service",
		}) {
			return
		}

		if err := sudoRun("systemctl", "daemon-reload").Run(); err != nil {
			emit(DeploymentEvent{
				Scope:   "step",
				Step:    "restart_service",
				Status:  "failed",
				Message: "Failed to reload systemd",
				Error:   err.Error(),
			})
			return
		}

		if err := sudoRun("systemctl", "restart", serviceName).Run(); err != nil {
			emit(DeploymentEvent{
				Scope:   "step",
				Step:    "restart_service",
				Status:  "failed",
				Message: "Failed to restart service",
				Error:   err.Error(),
			})
			return
		}

		emit(DeploymentEvent{
			Scope:   "step",
			Step:    "restart_service",
			Status:  "completed",
			Message: "Service restarted successfully",
		})

		emit(DeploymentEvent{
			Scope:   "action",
			Status:  "completed",
			Message: fmt.Sprintf("Deployed version %s successfully", req.ArtifactVersion),
		})
	}()
}


// TODO: make naming explictly binary app actions

// StartApp implements ServerInterface.
func (h *APIHandler) StartApp(w http.ResponseWriter, r *http.Request) {
	var req AppActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAppActionResponse(
			w,
			http.StatusBadRequest,
			"error",
			fmt.Sprintf("invalid request body: %v", err),
			"",
		)
		return
	}

	serviceName := fmt.Sprintf("%s-%s", req.AppSlug, req.Environment)
	output, err := sudoRun("systemctl", "start", serviceName).CombinedOutput()
	if err != nil {
		writeAppActionResponse(
			w,
			http.StatusInternalServerError,
			"error",
			fmt.Sprintf("failed to start service: %v", err),
			string(output),
		)
		return
	}

	writeAppActionResponse(
		w,
		http.StatusOK,
		"success",
		fmt.Sprintf("service %s started", serviceName),
		string(output),
	)
}

// StopApp implements ServerInterface.
func (h *APIHandler) StopApp(w http.ResponseWriter, r *http.Request) {
	var req AppActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAppActionResponse(
			w,
			http.StatusBadRequest,
			"error",
			fmt.Sprintf("invalid request body: %v", err),
			"",
		)
		return
	}

	serviceName := fmt.Sprintf("%s-%s", req.AppSlug, req.Environment)
	output, err := sudoRun("systemctl", "stop", serviceName).CombinedOutput()
	if err != nil {
		writeAppActionResponse(
			w,
			http.StatusInternalServerError,
			"error",
			fmt.Sprintf("failed to stop service: %v", err),
			string(output),
		)
		return
	}

	writeAppActionResponse(
		w,
		http.StatusOK,
		"success",
		fmt.Sprintf("service %s stopped", serviceName),
		string(output),
	)
}

// RestartApp implements ServerInterface.
func (h *APIHandler) RestartApp(w http.ResponseWriter, r *http.Request) {
	var req AppActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAppActionResponse(
			w,
			http.StatusBadRequest,
			"error",
			fmt.Sprintf("invalid request body: %v", err),
			"",
		)
		return
	}

	serviceName := fmt.Sprintf("%s-%s", req.AppSlug, req.Environment)
	output, err := sudoRun("systemctl", "restart", serviceName).CombinedOutput()
	if err != nil {
		writeAppActionResponse(
			w,
			http.StatusInternalServerError,
			"error",
			fmt.Sprintf("failed to restart service: %v", err),
			string(output),
		)
		return
	}

	writeAppActionResponse(
		w,
		http.StatusOK,
		"success",
		fmt.Sprintf("service %s restarted", serviceName),
		string(output),
	)
}

func createSystemdService(
	serviceName, binaryPath, workDir, configDir string,
	port int,
	args *[]string,
) error {
	servicePath := systemdServicePath(serviceName)

	var argsStr string
	if args != nil && len(*args) > 0 {
		argsStr = " " + strings.Join(*args, " ")
	}

	envFile := path.Join(configDir, "env")
	envFileDirective := ""
	if _, err := os.Stat(envFile); err == nil {
		envFileDirective = fmt.Sprintf("EnvironmentFile=%s\n", envFile)
	}

	serviceContent := fmt.Sprintf(`[Unit]
Description=%s service
After=network.target

[Service]
Type=simple
WorkingDirectory=%s
ExecStart=%s%s
Restart=always
RestartSec=5
Environment=PORT=%d
%s
[Install]
WantedBy=multi-user.target
`, serviceName, workDir, binaryPath, argsStr, port, envFileDirective)

	return sudoWriteFile(servicePath, []byte(serviceContent), 0o644)
}

func systemdServicePath(serviceName string) string {
	return path.Join("/etc/systemd/system", serviceName+".service")
}
