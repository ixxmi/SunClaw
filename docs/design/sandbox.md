# Docker Sandboxing for Shell Tool

## Overview

The Shell tool supports Docker-based sandboxing for secure command execution. When enabled, all shell commands run inside isolated Docker containers instead of directly on the host system.

## Configuration

### Enable Sandbox

Add the following to your `~/.goclaw/config.json`:

```json
{
  "tools": {
    "shell": {
      "enabled": true,
      "sandbox": {
        "enabled": true
      }
    }
  }
}
```

### Full Configuration Options

```json
{
  "tools": {
    "shell": {
      "enabled": true,
      "sandbox": {
        "enabled": true,
        "image": "goclaw/sandbox:latest",
        "workdir": "/workspace",
        "remove": true,
        "network": "none",
        "privileged": false
      }
    }
  }
}
```

### Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable/disable Docker sandboxing |
| `image` | string | `goclaw/sandbox:latest` | Docker image to use for containers |
| `workdir` | string | `/workspace` | Working directory inside the container |
| `remove` | bool | `true` | Automatically remove container after execution |
| `network` | string | `none` | Network mode (`none`, `bridge`, `host`) |
| `privileged` | bool | `false` | Run container in privileged mode |

## Building the Sandbox Image

### Dockerfile

Create a `Dockerfile` with the following content:

```dockerfile
FROM alpine:latest

# Install common tools
RUN apk add --no-cache \
    bash \
    curl \
    wget \
    python3 \
    py3-pip \
    nodejs \
    npm \
    git \
    jq \
    coreutils \
    grep \
    sed \
    awk \
    ca-certificates

# Set working directory
WORKDIR /workspace

CMD ["/bin/sh"]
```

### Build the Image

```bash
# Build the image
docker build -t goclaw/sandbox:latest .

# Or push to Docker Hub for remote use
docker tag goclaw/sandbox:latest <username>/goclaw-sandbox:latest
docker push <username>/goclaw-sandbox:latest
```

### Using a Pre-built Image

If you're using a custom image from Docker Hub, update the config:

```json
{
  "tools": {
    "shell": {
      "sandbox": {
        "enabled": true,
        "image": "<username>/goclaw-sandbox:latest"
      }
    }
  }
}
```

## How It Works

### Execution Flow

1. **Initialization**: When sandbox is enabled, a Docker client is initialized
2. **Container Creation**: Each command creates a new container with:
   - Workspace directory mounted from host
   - Network isolation (by default)
   - Auto-removal after completion
3. **Command Execution**: The command runs inside the container
4. **Output Capture**: stdout and stderr are captured and returned
5. **Cleanup**: Container is removed (if `remove: true`)

### Workspace Mounting

The workspace directory is mounted into the container at the path specified by `workdir`:

| Host Path | Container Path |
|-----------|----------------|
| `~/.goclaw/workspace` | `/workspace` |

Files created in the workspace are accessible on the host system.

### Network Modes

| Mode | Description | Use Case |
|------|-------------|----------|
| `none` | No network access (default) | Maximum security, offline operations |
| `bridge` | Isolated bridge network | Allow outbound network access |
| `host` | Host network | Full network access (not recommended) |

## Security Considerations

### Recommended Settings

```json
{
  "tools": {
    "shell": {
      "sandbox": {
        "enabled": true,
        "network": "none",
        "privileged": false,
        "remove": true
      }
    }
  }
}
```

### Security Benefits

- **Isolation**: Commands run in isolated containers
- **Network Isolation**: Default `none` mode prevents network access
- **No Privilege Escalation**: Default non-privileged mode
- **Automatic Cleanup**: Containers are removed after execution
- **Resource Limits**: Docker enforces resource constraints

### Potential Risks

- **File Access**: Workspace is mounted with full read/write access
- **Privileged Mode**: Never enable `privileged: true` in production
- **Host Network**: Avoid `network: "host"` unless necessary

## Usage Examples

### Basic Command Execution

```bash
# With sandbox enabled
goclaw chat
➤ echo "Hello, World!"
[Tool: exec]
[Result: Hello, World!]
```

### Python Script Execution

```bash
➤ python3 -c "print(2 ** 10)"
[Tool: exec]
[Result: 1024]
```

### Network Restrictions

With `network: "none"`:

```bash
➤ curl https://example.com
[Tool: exec]
[Result: curl: (6) Could not resolve host: example.com]
```

## Troubleshooting

### Docker Not Running

```
Failed to initialize Docker client, sandbox disabled
```

**Solution**: Start Docker Desktop or Docker Daemon.

### Image Not Found

```
failed to create container: Error response from daemon: pull access denied for goclaw/sandbox
```

**Solution**: Build or pull the required image.

### Permission Denied

```
failed to start container: permission denied
```

**Solution**: Ensure your user has Docker permissions.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        goclaw Agent                         │
│                                                              │
│  ┌──────────────┐         ┌──────────────────────────────┐ │
│  │  Shell Tool  │ ──────► │  Docker API                  │ │
│  │              │         │                              │ │
│  │  exec()      │         │  ContainerCreate()           │ │
│  │  ┌────────┐  │         │  ContainerStart()            │ │
│  │  │Direct  │  │         │  ContainerWait()             │ │
│  │  │        │  │         │  ContainerLogs()             │ │
│  │  └────────┘  │         │  ContainerRemove()           │ │
│  │  ┌────────┐  │         │                              │ │
│  │  │Sandbox │  │         │  ┌────────────────────────┐  │ │
│  │  │        │  │         │  │ goclaw/sandbox:latest │  │ │
│  │  └────────┘  │         │  │  - bash                │  │ │
│  └──────────────┘         │  │  - python3             │  │ │
│                            │  │  - nodejs              │  │ │
│                            │  └────────────────────────┘  │ │
│                            └──────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

## API Reference

### ShellTool Structure

```go
type ShellTool struct {
    enabled       bool
    allowedCmds   []string
    deniedCmds    []string
    timeout       time.Duration
    workingDir    string
    sandboxConfig config.SandboxConfig
    dockerClient  *client.Client
}
```

### SandboxConfig Structure

```go
type SandboxConfig struct {
    Enabled    bool   `mapstructure:"enabled"`
    Image      string `mapstructure:"image"`
    Workdir    string `mapstructure:"workdir"`
    Remove     bool   `mapstructure:"remove"`
    Network    string `mapstructure:"network"`
    Privileged bool   `mapstructure:"privileged"`
}
```

## See Also

- [Configuration Guide](./Configuration.md)
- [Shell Tool](./Shell.md)
- [Security Best Practices](./Security.md)
