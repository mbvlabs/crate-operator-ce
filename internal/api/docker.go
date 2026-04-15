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

const (
	CreateDockerAppAction = "create_docker_app"
	DeployDockerAppAction = "deploy_docker_app"
)

var CreateDockerAppRequestSchema = z.Struct(z.Shape{
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
		Required(z.Message("Domain must be provided")).
		Max(253, z.Message("Domain must be between 1 and 253 characters")),
	"port": z.Int().Required(z.Message("Port must be provided")).
		GT(0).
		LT(65536).
		Not().OneOf([]int{80, 443, 9640}, z.Message("Ports: 80, 443, 9640 is reserved")),
})

var DeployDockerAppRequestSchema = z.Struct(z.Shape{
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

func dockerImageRef(source, tag string) string {
	return strings.TrimRight(source, "/") + ":" + tag
}

func createDockerSystemdService(
	serviceName, image, configDir string,
	port int,
	command []string,
	extraPorts []string,
	volumes []string,
) error {
	servicePath := systemdServicePath(serviceName)

	envFile := path.Join(configDir, "env")
	envFileFlag := ""
	if _, err := os.Stat(envFile); err == nil {
		envFileFlag = fmt.Sprintf(" --env-file %s", envFile)
	}

	var portFlags strings.Builder
	fmt.Fprintf(&portFlags, " -p %d:%d", port, port)
	for _, p := range extraPorts {
		fmt.Fprintf(&portFlags, " -p %s", p)
	}

	var volumeFlags strings.Builder
	for _, v := range volumes {
		fmt.Fprintf(&volumeFlags, " -v %s", v)
	}

	var cmdStr string
	if len(command) > 0 {
		cmdStr = " " + strings.Join(command, " ")
	}

	serviceContent := fmt.Sprintf(`[Unit]
Description=%s docker service
After=docker.service network.target
Requires=docker.service

[Service]
Type=simple
Restart=always
RestartSec=5
ExecStartPre=-/usr/bin/docker stop %s
ExecStartPre=-/usr/bin/docker rm %s
ExecStartPre=/usr/bin/docker pull %s
ExecStart=/usr/bin/docker run --rm --name %s%s%s%s %s%s
ExecStop=/usr/bin/docker stop %s

[Install]
WantedBy=multi-user.target
`,
		serviceName,
		serviceName,
		serviceName,
		image,
		serviceName,
		portFlags.String(),
		volumeFlags.String(),
		envFileFlag,
		image,
		cmdStr,
		serviceName,
	)

	return sudoWriteFile(servicePath, []byte(serviceContent), 0o644)
}

// CreateDockerApp implements ServerInterface.
func (h *APIHandler) CreateDockerApp(w http.ResponseWriter, r *http.Request) {
	var req CreateDockerAppRequest
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
	if validationErrors := CreateDockerAppRequestSchema.Validate(&req); validationErrors != nil {
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

	writeDeployResponse(w, http.StatusAccepted, "accepted", "docker app creation accepted", "")

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		groupingID := xid.New().String()
		emitter := NewCallbackEmitter(req.CallbackUrl, h.apiKey)

		emit := func(ev DeploymentEvent) bool {
			ev.GroupingID = groupingID
			ev.Action = CreateDockerAppAction
			if err := emitter.EmitDeploymentEvent(ctx, ev); err != nil {
				slog.Error("failed to emit deployment event", "error", err)
				return false
			}
			return true
		}

		if !emit(DeploymentEvent{
			Scope:   "action",
			Status:  "in_progress",
			Message: fmt.Sprintf("Starting docker deployment for %s/%s", req.AppSlug, req.EnvironmentName),
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

		if err := os.MkdirAll(appDir, 0o755); err != nil {
			emit(DeploymentEvent{
				Scope: "step", Step: "create_app_directory",
				Status: "failed", Message: "Failed to create app directory", Error: err.Error(),
			})
			return
		}
		if err := sudoMkdirAll(configDir); err != nil {
			emit(DeploymentEvent{
				Scope: "step", Step: "create_config_directory",
				Status: "failed", Message: "Failed to create config directory", Error: err.Error(),
			})
			return
		}
		emit(DeploymentEvent{
			Scope: "step", Step: "create_app_directory",
			Status: "completed", Message: "App and config directories created",
		})

		image := dockerImageRef(req.ArtifactSource, req.ArtifactVersion)
		if !emit(DeploymentEvent{
			Scope: "step", Step: "pull_image",
			Status: "in_progress", Message: fmt.Sprintf("Pulling image %s", image),
		}) {
			return
		}
		if out, err := sudoRun("docker", "pull", image).CombinedOutput(); err != nil {
			emit(DeploymentEvent{
				Scope: "step", Step: "pull_image",
				Status: "failed", Message: "Failed to pull docker image",
				Error: fmt.Sprintf("%v: %s", err, string(out)),
			})
			return
		}
		emit(DeploymentEvent{
			Scope: "step", Step: "pull_image",
			Status: "completed", Message: "Image pulled",
		})

		if req.EnvVars != nil && len(*req.EnvVars) > 0 {
			envPath := path.Join(configDir, "env")
			var envContent strings.Builder
			for key, value := range *req.EnvVars {
				fmt.Fprintf(&envContent, "%s=%s\n", key, value)
			}
			if err := sudoWriteFile(envPath, []byte(envContent.String()), 0o640); err != nil {
				emit(DeploymentEvent{
					Scope: "step", Step: "environmental_variables",
					Status: "failed", Message: "Failed to write env file", Error: err.Error(),
				})
				return
			}
			emit(DeploymentEvent{
				Scope: "step", Step: "environmental_variables",
				Status: "completed", Message: "Environment variables configured",
			})
		}

		serviceName := strings.ToLower(
			fmt.Sprintf("%s--%s--%s", req.TeamSlug, req.AppSlug, req.EnvironmentName),
		)
		var command, extraPorts, volumes []string
		if req.DockerCommand != nil {
			command = *req.DockerCommand
		}
		if req.Ports != nil {
			extraPorts = *req.Ports
		}
		if req.Volumes != nil {
			volumes = *req.Volumes
		}

		if err := createDockerSystemdService(
			serviceName, image, configDir, req.Port, command, extraPorts, volumes,
		); err != nil {
			emit(DeploymentEvent{
				Scope: "step", Step: "systemd_service",
				Status: "failed", Message: "Failed to create systemd service", Error: err.Error(),
			})
			return
		}
		emit(DeploymentEvent{
			Scope: "step", Step: "systemd_service",
			Status: "completed", Message: "Systemd service created",
		})

		if err := sudoRun("systemctl", "daemon-reload").Run(); err != nil {
			emit(DeploymentEvent{
				Scope: "step", Step: "start_service",
				Status: "failed", Message: "Failed to reload systemd", Error: err.Error(),
			})
			return
		}
		if err := sudoRun("systemctl", "enable", serviceName).Run(); err != nil {
			emit(DeploymentEvent{
				Scope: "step", Step: "start_service",
				Status: "failed", Message: "Failed to enable service", Error: err.Error(),
			})
			return
		}
		if err := sudoRun("systemctl", "start", serviceName).Run(); err != nil {
			emit(DeploymentEvent{
				Scope: "step", Step: "start_service",
				Status: "failed", Message: "Failed to start service", Error: err.Error(),
			})
			return
		}
		emit(DeploymentEvent{
			Scope: "step", Step: "start_service",
			Status: "completed", Message: "Service started",
		})

		caddyManager := NewCaddyManager()
		if err := caddyManager.ConfigureRoute(req.Domain, req.Port); err != nil {
			emit(DeploymentEvent{
				Scope: "step", Step: "caddy_configuration",
				Status: "failed", Message: "Failed to configure Caddy route", Error: err.Error(),
			})
			return
		}
		emit(DeploymentEvent{
			Scope: "step", Step: "caddy_configuration",
			Status: "completed", Message: "Caddy route configured",
		})

		emit(DeploymentEvent{
			Scope: "action", Status: "completed", Message: "Docker deployment completed",
		})
	}()
}

// DeployDockerApp implements ServerInterface.
func (h *APIHandler) DeployDockerApp(w http.ResponseWriter, r *http.Request) {
	var req DeployDockerAppRequest
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
	if validationErrors := DeployDockerAppRequestSchema.Validate(&req); validationErrors != nil {
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

	writeDeployResponse(w, http.StatusAccepted, "accepted", "docker app deployment accepted", "")

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		groupingID := xid.New().String()
		emitter := NewCallbackEmitter(req.CallbackUrl, h.apiKey)

		emit := func(ev DeploymentEvent) bool {
			ev.GroupingID = groupingID
			ev.Action = DeployDockerAppAction
			if err := emitter.EmitDeploymentEvent(ctx, ev); err != nil {
				slog.Error("failed to emit deployment event", "error", err)
				return false
			}
			return true
		}

		if !emit(DeploymentEvent{
			Scope:   "action",
			Status:  "in_progress",
			Message: fmt.Sprintf("Starting docker deployment for %s/%s", req.AppSlug, req.EnvironmentName),
		}) {
			return
		}

		configDir := path.Join(
			appsConfigDir(),
			strings.ToLower(req.TeamSlug)+"-"+strings.ToLower(req.AppId),
			strings.ToLower(req.EnvironmentName),
		)
		serviceName := strings.ToLower(
			fmt.Sprintf("%s--%s--%s", req.TeamSlug, req.AppSlug, req.EnvironmentName),
		)
		servicePath := systemdServicePath(serviceName)

		prevContent, err := os.ReadFile(servicePath)
		if err != nil {
			emit(DeploymentEvent{
				Scope: "step", Step: "switch",
				Status: "failed", Message: "Failed to read current service file",
				Error: err.Error(),
			})
			return
		}

		port, command, extraPorts, volumes := parseDockerServiceUnit(string(prevContent))

		image := dockerImageRef(req.ArtifactSource, req.ArtifactVersion)
		if !emit(DeploymentEvent{
			Scope: "step", Step: "pull_image",
			Status: "in_progress", Message: fmt.Sprintf("Pulling image %s", image),
		}) {
			return
		}
		if out, err := sudoRun("docker", "pull", image).CombinedOutput(); err != nil {
			emit(DeploymentEvent{
				Scope: "step", Step: "pull_image",
				Status: "failed", Message: "Failed to pull docker image",
				Error: fmt.Sprintf("%v: %s", err, string(out)),
			})
			return
		}
		emit(DeploymentEvent{
			Scope: "step", Step: "pull_image",
			Status: "completed", Message: "Image pulled",
		})

		if req.EnvVars != nil && len(*req.EnvVars) > 0 {
			envPath := path.Join(configDir, "env")
			var envContent strings.Builder
			for key, value := range *req.EnvVars {
				fmt.Fprintf(&envContent, "%s=%s\n", key, value)
			}
			if err := sudoWriteFile(envPath, []byte(envContent.String()), 0o640); err != nil {
				emit(DeploymentEvent{
					Scope: "step", Step: "environmental_variables",
					Status: "failed", Message: "Failed to write env file", Error: err.Error(),
				})
				return
			}
			emit(DeploymentEvent{
				Scope: "step", Step: "environmental_variables",
				Status: "completed", Message: "Environment variables updated",
			})
		}

		if err := createDockerSystemdService(
			serviceName, image, configDir, port, command, extraPorts, volumes,
		); err != nil {
			emit(DeploymentEvent{
				Scope: "step", Step: "switch",
				Status: "failed", Message: "Failed to update systemd service", Error: err.Error(),
			})
			return
		}
		emit(DeploymentEvent{
			Scope: "step", Step: "switch",
			Status: "completed", Message: "Systemd service updated",
		})

		if err := sudoRun("systemctl", "daemon-reload").Run(); err != nil {
			emit(DeploymentEvent{
				Scope: "step", Step: "restart_service",
				Status: "failed", Message: "Failed to reload systemd", Error: err.Error(),
			})
			return
		}
		if err := sudoRun("systemctl", "restart", serviceName).Run(); err != nil {
			emit(DeploymentEvent{
				Scope: "step", Step: "restart_service",
				Status: "failed", Message: "Failed to restart service", Error: err.Error(),
			})
			return
		}
		emit(DeploymentEvent{
			Scope: "step", Step: "restart_service",
			Status: "completed", Message: "Service restarted successfully",
		})

		emit(DeploymentEvent{
			Scope:   "action",
			Status:  "completed",
			Message: fmt.Sprintf("Deployed image %s successfully", image),
		})
	}()
}

// parseDockerServiceUnit extracts the host port, command tail, extra port mappings,
// and volume mounts from a previously written docker systemd unit so we can preserve
// them across redeploys.
func parseDockerServiceUnit(content string) (port int, command, extraPorts, volumes []string) {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "ExecStart=") {
			continue
		}
		tokens := strings.Fields(strings.TrimPrefix(trimmed, "ExecStart="))
		var imageSeen bool
		for i := 0; i < len(tokens); i++ {
			t := tokens[i]
			switch {
			case t == "-p" && i+1 < len(tokens):
				spec := tokens[i+1]
				if port == 0 {
					if idx := strings.Index(spec, ":"); idx > 0 {
						_, _ = fmt.Sscanf(spec[:idx], "%d", &port)
					}
				} else {
					extraPorts = append(extraPorts, spec)
				}
				i++
			case t == "-v" && i+1 < len(tokens):
				volumes = append(volumes, tokens[i+1])
				i++
			case t == "--env-file" && i+1 < len(tokens):
				i++
			case t == "--rm" || t == "-d":
				// flag with no value
			case t == "--name" && i+1 < len(tokens):
				i++
			case !strings.HasPrefix(t, "-") && !imageSeen && !strings.HasSuffix(t, "docker") && !strings.HasSuffix(t, "run"):
				imageSeen = true
			case imageSeen:
				command = append(command, t)
			}
		}
		break
	}
	return port, command, extraPorts, volumes
}
