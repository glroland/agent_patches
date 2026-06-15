# Central Helm Chart

Deploys central-backend and central-ui to Kubernetes.

## Structure

```
deploy/helm/
├── Chart.yaml           # Parent chart with dependencies
├── values.yaml          # Default values for both components
├── central-backend/     # Backend subchart
│   ├── Chart.yaml
│   ├── values.yaml
│   └── templates/
│       ├── deployment.yaml
│       └── service.yaml
└── central-ui/          # UI subchart
    ├── Chart.yaml
    ├── values.yaml
    └── templates/
        ├── deployment.yaml
        └── service.yaml
```

## Usage

```bash
# Build container images first
docker build -t central-backend:latest central-backend/
docker build -t central-ui:latest central-ui/

# Install the chart
helm install central ./deploy/helm

# Upgrade
helm upgrade central ./deploy/helm

# Uninstall
helm uninstall central
```

## Configuration

Edit `deploy/helm/values.yaml` to customize:

- Image repositories and tags
- Container/service ports
- Environment variables
- Resource limits
- Node selectors, tolerations, affinity
