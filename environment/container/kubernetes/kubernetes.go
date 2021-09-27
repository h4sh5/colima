package kubernetes

import (
	"github.com/abiosoft/colima/cli"
	"github.com/abiosoft/colima/config"
	"github.com/abiosoft/colima/environment"
	"github.com/abiosoft/colima/environment/container/containerd"
	"github.com/abiosoft/colima/environment/container/docker"
	"strings"
)

// Name is container runtime name
const Name = "kubernetes"

func newRuntime(host environment.HostActions, guest environment.GuestActions) environment.Container {
	return &kubernetesRuntime{
		host:         host,
		guest:        guest,
		CommandChain: cli.New("kubernetes"),
	}
}

func init() {
	environment.RegisterContainer(Name, newRuntime)
}

var _ environment.Container = (*kubernetesRuntime)(nil)

type kubernetesRuntime struct {
	host  environment.HostActions
	guest environment.GuestActions
	cli.CommandChain
}

func (c kubernetesRuntime) Name() string {
	return Name
}

func (c kubernetesRuntime) isInstalled() bool {
	// it is installed if uninstall script is present.
	return c.guest.Run("command", "-v", "k3s-uninstall.sh") == nil
}

func (c kubernetesRuntime) Running() bool {
	return c.guest.Run("service", "k3s", "status") == nil
}

func (c kubernetesRuntime) runtime() string {
	return c.guest.Get(environment.ContainerRuntimeKey)
}
func (c kubernetesRuntime) kubernetesVersion() string {
	return c.guest.Get(environment.KubernetesVersionKey)
}

func (c *kubernetesRuntime) Provision() error {
	r := c.Init()

	if c.isInstalled() {
		return nil
	}

	r.Stage("provisioning")

	// k3s
	r.Stage("installing")
	installK3s(c.host, c.guest, r, c.runtime())

	return r.Exec()
}

func (c kubernetesRuntime) Start() error {
	r := c.Init()
	if c.Running() {
		r.Println("already running")
		return nil
	}

	r.Stage("starting")

	r.Add(func() error {
		return c.guest.Run("sudo", "service", "k3s", "start")
	})

	if err := r.Exec(); err != nil {
		return err
	}

	return c.provisionKubeconfig()
}

func (c kubernetesRuntime) Stop() error {
	r := c.Init()
	r.Stage("stopping")
	r.Add(func() error {
		return c.guest.Run("k3s-killall.sh")
	})

	// k3s is buggy with external containerd for now
	// cleanup is manual
	r.Add(func() error {
		return c.stopAllContainers()
	})

	return r.Exec()
}

func (c kubernetesRuntime) deleteAllContainers() error {
	ids := c.runningContainerIDs()
	if ids == "" {
		return nil
	}

	var args []string

	switch c.runtime() {
	case containerd.Name:
		args = []string{"nerdctl", "-n", "k8s.io", "rm", "-f"}
	case docker.Name:
		args = []string{"docker", "rm", "-f"}
	default:
		return nil
	}

	args = append(args, strings.Fields(ids)...)

	return c.guest.Run("sudo", "sh", "-c", strings.Join(args, " "))
}

func (c kubernetesRuntime) stopAllContainers() error {

	ids := c.runningContainerIDs()
	if ids == "" {
		return nil
	}

	var args []string

	switch c.runtime() {
	case containerd.Name:
		args = []string{"nerdctl", "-n", "k8s.io", "kill"}
	case docker.Name:
		args = []string{"docker", "kill"}
	default:
		return nil
	}

	args = append(args, strings.Fields(ids)...)

	return c.guest.Run("sudo", "sh", "-c", strings.Join(args, " "))
}

func (c kubernetesRuntime) runningContainerIDs() string {
	var args []string

	switch c.runtime() {
	case containerd.Name:
		args = []string{"sudo", "nerdctl", "-n", "k8s.io", "ps", "-q"}
	case docker.Name:
		args = []string{"sudo", "sh", "-c", `docker ps --format '{{.Names}}'| grep "k8s_"`}
	default:
		return ""
	}

	ids, _ := c.guest.RunOutput(args...)
	if ids == "" {
		return ""
	}
	return strings.ReplaceAll(ids, "\n", " ")
}

func (c kubernetesRuntime) Teardown() error {
	r := c.Init()
	r.Stage("deleting")

	if c.isInstalled() {
		r.Add(func() error {
			return c.guest.Run("k3s-uninstall.sh")
		})
	}

	// k3s is buggy with external containerd for now
	// cleanup is manual
	r.Add(func() error {
		return c.deleteAllContainers()
	})

	c.teardownKubeconfig(r)
	r.Add(func() error {
		return c.guest.Set(kubeconfigKey, "")
	})

	return r.Exec()
}

func (c kubernetesRuntime) Dependencies() []string {
	return []string{"kubectl"}
}

func (c kubernetesRuntime) Version() string {
	version, _ := c.host.RunOutput("kubectl", "--context", config.AppName(), "version", "--short")
	return version
}
