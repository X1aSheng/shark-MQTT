package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── Docker ────────────────────────────────────────────────

func TestDockerfileExists(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "docker", "Dockerfile")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("Dockerfile not found at deploy/docker/Dockerfile")
	}
}

func TestDockerfileBuildsCorrectTarget(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "docker", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "./cmd/") {
		t.Error("Dockerfile should build ./cmd/ as the production binary")
	}
}

func TestDockerfileHasAllPorts(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "docker", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	required := []string{"1883", "8883", "9090"}
	for _, port := range required {
		if !strings.Contains(content, port) {
			t.Errorf("Dockerfile missing port %s in EXPOSE", port)
		}
	}
}

func TestDockerfileHasHealthcheck(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "docker", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "HEALTHCHECK") {
		t.Error("Dockerfile should have a HEALTHCHECK instruction")
	}
}

func TestDockerfileHasNonRootUser(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "docker", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "adduser") && !strings.Contains(content, "USER") {
		t.Error("Dockerfile should create and use a non-root user")
	}
}

func TestDockerComposeExists(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "docker", "docker-compose.yml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("docker-compose.yml not found at deploy/docker/docker-compose.yml")
	}
}

func TestDockerComposeHasAllPorts(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "docker", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	required := []string{"1883:1883", "9090:9090"}
	for _, port := range required {
		if !strings.Contains(content, port) {
			t.Errorf("docker-compose.yml missing port mapping %s", port)
		}
	}
}

func TestDockerComposeHasHealthcheck(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "docker", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "healthcheck") {
		t.Error("docker-compose.yml should have a healthcheck configuration")
	}
}

func TestDockerComposeTestExists(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "docker", "docker-compose.test.yml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("docker-compose.test.yml not found at deploy/docker/docker-compose.test.yml")
	}
}

func TestDockerignoreExists(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "docker", ".dockerignore")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal(".dockerignore not found at deploy/docker/.dockerignore")
	}
}

func TestPrometheusDockerConfigExists(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "docker", "prometheus.yml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("prometheus.yml not found at deploy/docker/prometheus.yml")
	}
}

// ─── Kubernetes ────────────────────────────────────────────

func TestK8sManifestsExist(t *testing.T) {
	base := filepath.Join("..", "..", "deploy", "k8s", "app")
	files := []string{
		"namespace.yaml", "configmap.yaml", "deployment.yaml",
		"service.yaml", "hpa.yaml", "networkpolicy.yaml",
		"kustomization.yaml", "ingress.yaml",
	}
	for _, f := range files {
		path := filepath.Join(base, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Missing K8s manifest: %s", f)
		}
	}
}

func TestK8sDeploymentHasAllPorts(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "k8s", "app", "deployment.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "containerPort: 1883") {
		t.Error("deployment.yaml missing MQTT port 1883")
	}
	if !strings.Contains(content, "containerPort: 9090") {
		t.Error("deployment.yaml missing metrics port 9090")
	}
}

func TestK8sDeploymentHasSecurityContext(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "k8s", "app", "deployment.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "runAsNonRoot") {
		t.Error("deployment.yaml should have PodSecurityContext with runAsNonRoot")
	}
	if !strings.Contains(content, "allowPrivilegeEscalation") {
		t.Error("deployment.yaml should have ContainerSecurityContext with allowPrivilegeEscalation")
	}
}

func TestK8sDeploymentHasProbes(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "k8s", "app", "deployment.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "livenessProbe") {
		t.Error("deployment.yaml missing livenessProbe")
	}
	if !strings.Contains(content, "readinessProbe") {
		t.Error("deployment.yaml missing readinessProbe")
	}
	if !strings.Contains(content, "preStop") {
		t.Error("deployment.yaml missing preStop lifecycle hook")
	}
}

func TestK8sServiceHasMQTTPort(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "k8s", "app", "service.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "name: mqtt") {
		t.Error("service.yaml missing MQTT port definition")
	}
}

func TestK8sHPAExists(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "k8s", "app", "hpa.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "HorizontalPodAutoscaler") {
		t.Error("hpa.yaml should contain a HorizontalPodAutoscaler")
	}
	if !strings.Contains(content, "minReplicas") || !strings.Contains(content, "maxReplicas") {
		t.Error("hpa.yaml missing minReplicas/maxReplicas")
	}
}

func TestK8sNetworkPolicyExists(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "k8s", "app", "networkpolicy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "1883") {
		t.Error("networkpolicy.yaml should allow MQTT port 1883")
	}
	if !strings.Contains(content, "9090") {
		t.Error("networkpolicy.yaml should allow metrics port 9090")
	}
}

func TestK8sAppConfigmapExists(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "k8s", "app", "configmap.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "shark-mqtt-config") {
		t.Error("App configmap should be named shark-mqtt-config")
	}
	if !strings.Contains(content, "config.yaml") {
		t.Error("configmap should contain config.yaml data")
	}
}

func TestK8sInfraPrometheusExists(t *testing.T) {
	base := filepath.Join("..", "..", "deploy", "k8s", "infra", "prometheus")
	files := []string{"deployment.yaml", "service.yaml", "configmap.yaml"}
	for _, f := range files {
		path := filepath.Join(base, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Missing prometheus manifest: %s", f)
		}
	}
}

// ─── Helm ─────────────────────────────────────────────────

const helmChartDir = "../../deploy/k8s/helm/shark-mqtt"

func TestHelmChartExists(t *testing.T) {
	files := []string{"Chart.yaml", "values.yaml", "values-prod.yaml", "README.md"}
	for _, f := range files {
		path := filepath.Join(helmChartDir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Helm chart missing file: %s", f)
		}
	}
}

func TestHelmTemplatesExist(t *testing.T) {
	templates := []string{
		"_helpers.tpl", "deployment.yaml", "service.yaml",
		"configmap.yaml", "ingress.yaml", "hpa.yaml",
		"networkpolicy.yaml", "servicemonitor.yaml", "NOTES.txt",
	}
	for _, f := range templates {
		path := filepath.Join(helmChartDir, "templates", f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Helm chart missing template: %s", f)
		}
	}
}

func TestHelmChartYamlValid(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(helmChartDir, "Chart.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "name: shark-mqtt") {
		t.Error("Chart.yaml should have name: shark-mqtt")
	}
	if !strings.Contains(content, "apiVersion: v2") {
		t.Error("Chart.yaml should have apiVersion: v2")
	}
}

func TestHelmValuesHasConfig(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(helmChartDir, "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	required := []string{"config:", "autoscaling:", "networkPolicy:", "resources:"}
	for _, section := range required {
		if !strings.Contains(content, section) {
			t.Errorf("values.yaml missing section: %s", section)
		}
	}
}

func TestHelmHelpersDefinesLabels(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(helmChartDir, "templates", "_helpers.tpl"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "shark-mqtt.labels") {
		t.Error("_helpers.tpl should define shark-mqtt.labels template")
	}
	if !strings.Contains(content, "shark-mqtt.selectorLabels") {
		t.Error("_helpers.tpl should define shark-mqtt.selectorLabels template")
	}
}

func TestHelmNotesHasEndpoints(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(helmChartDir, "templates", "NOTES.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "shark-mqtt") {
		t.Error("NOTES.txt should reference shark-mqtt")
	}
}

// ─── Cleanup verification ─────────────────────────────────

func TestOldK8sDirRemoved(t *testing.T) {
	path := filepath.Join("..", "..", "k8s")
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		t.Error("Old k8s/ directory should have been removed")
	}
}

func TestOldDockerfileRemoved(t *testing.T) {
	path := filepath.Join("..", "..", "Dockerfile")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Old root-level Dockerfile should have been removed")
	}
}

func TestDeployReadmeExists(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "README.md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("deploy/README.md not found")
	}
}

func TestEntryPointExists(t *testing.T) {
	entries, err := filepath.Glob(filepath.Join("..", "..", "cmd", "*", "main.go"))
	if err != nil || len(entries) == 0 {
		// Fallback: check cmd/ directly
		matches, _ := filepath.Glob(filepath.Join("..", "..", "cmd", "*.go"))
		if len(matches) == 0 {
			t.Fatal("No entry point found in cmd/")
		}
	}
}
