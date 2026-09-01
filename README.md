# Talos Linux on Proxmox via Pulumi (Go)

This project provides a fully automated Infrastructure as Code (IaC) pipeline to bootstrap a highly available [Talos Linux](https://www.talos.dev/) Kubernetes cluster on Proxmox VE using [Pulumi](https://www.pulumi.com/) and Go.

By leveraging Proxmox **Cloud-Init (Snippets)** and the Talos `nocloud` ISO, this deployment completely eliminates the traditional DHCP "chicken-and-egg" network problem. Nodes are provisioned with permanent static IPs from their very first boot, and the Talos configuration is injected directly via a virtual CD-ROM drive.

## 🏗 Architecture & Features

* **Language:** Go
* **Providers:** `muhlba91/proxmoxve` and `pulumiverse/talos`
* **Networking:** Pure static IPs assigned via Proxmox Cloud-Init. No DHCP server required.
* **Configuration:** YAML-driven node inventory (`nodes.yaml`).
* **Bootstrapping:** Automated `etcd` bootstrap strictly targeting the first control plane node.

## 📋 Prerequisites

1. **Pulumi CLI** and **Go** installed on your local machine.
2. **Proxmox VE** up and running.
3. **Talos NoCloud ISO:** Download the Talos `nocloud` ISO (with QEMU guest agent enabled) from the [Talos Image Factory](https://factory.talos.dev/) and upload it to your Proxmox ISO datastore (e.g., `local`).

### ⚠️ Critical Proxmox Configuration: Enable Snippets
Pulumi passes the generated Talos Kubernetes configuration to Proxmox as a file. Proxmox needs permission to store this file as a "Snippet".
1. Open your Proxmox Web UI.
2. Go to **Datacenter** -> **Storage** -> Select your target storage (usually `local`) -> **Edit**.
3. Under the **Content** dropdown, ensure **Snippets** is selected.

## ⚙️ Configuration

Create or modify the `nodes.yaml` file in the root of this repository to define your cluster geometry.

```yaml
cluster_name: "talos-proxmox-cluster"
proxmox_node: "pve"
iso_file_id: "local:iso/talos-amd64.iso"
nodes:
  - name: "cp-01"
    role: "controlplane"
    ip: "192.168.20.10"
    cidr: 24
    gateway: "192.168.20.1"
    dns: "1.1.1.1"
    vlan: 20
    datastore: "local-lvm"
    disksize: 20
    cpu: 2
    memory: 4096
```

## 🚀 Deployment
### 1. Set Environment Variables

The Proxmox provider requires authentication credentials to communicate with your hypervisor. Export these to your terminal session:

```bash
export PROXMOX_VE_ENDPOINT="https://<YOUR_PROXMOX_IP>:8006/"
export PROXMOX_VE_USERNAME="root@pam"
export PROXMOX_VE_PASSWORD="<YOUR_PASSWORD>"
export PROXMOX_VE_INSECURE="true" # Required if using self-signed certs

# Set password inside quotes, leave empty for now password.
# This is required for "pulumi stack output kubeconfig" to work later on.
export PULUMI_CONFIG_PASSPHRASE=""
```

### 2. Initialize and Deploy

Initialize your Pulumi stack, download the Go modules, and run the deployment:

```bash
pulumi stack init dev
go mod tidy
pulumi up
```

Review the planned infrastructure changes and type yes to deploy. Pulumi will create the VMs, inject the static network config via Cloud-Init, generate the Talos machine configs as snippets, and automatically bootstrap the etcd cluster.
🔑 Accessing the Cluster

Once the pulumi up command finishes successfully, the generated kubeconfig is securely encrypted and stored within your Pulumi state.

To extract it and connect to your new Kubernetes cluster:
```bash

# 1. Extract talosconfig and kubeconfig from the Pulumi stack
pulumi stack output kubeconfig --show-secrets > kubeconfig.yaml
pulumi stack output talosconfig --show-secrets > talosconfig

# 2. Point kubectl to the extracted file
export KUBECONFIG=$PWD/kubeconfig.yaml

# 3. Verify access
kubectl get nodes -o wide
```
(Note: It may take 60-90 seconds after Pulumi finishes for the Talos API and Kubernetes API to become fully responsive).

## Cilium CNI

```bash
# 1. Set CNI none
talosctl patch mc --nodes <controlplane-ip> --patch @patch.yaml

# 2. Patch all nodes
talosctl patch mc --nodes <controlplane-ip, worker-ip(s)> --patch @patch-2.yaml

# 3. Enable Talos API access
talosctl patch mc --nodes <controlplane-ip, worker-ip(s)> --patch @patch-3.yaml

# 4. Install Cilium
helm install \
    cilium \
    cilium/cilium \
    --version 1.18.0 \
    --namespace kube-system \
    --set ipam.mode=kubernetes \
    --set kubeProxyReplacement=true \
    --set securityContext.capabilities.ciliumAgent="{CHOWN,KILL,NET_ADMIN,NET_RAW,IPC_LOCK,SYS_ADMIN,SYS_RESOURCE,DAC_OVERRIDE,FOWNER,SETGID,SETUID}" \
    --set securityContext.capabilities.cleanCiliumState="{NET_ADMIN,SYS_ADMIN,SYS_RESOURCE}" \
    --set cgroup.autoMount.enabled=false \
    --set cgroup.hostRoot=/sys/fs/cgroup \
    --set k8sServiceHost=localhost \
    --set k8sServicePort=7445 \
    --set gatewayAPI.enabled=true \
    --set gatewayAPI.enableAlpn=true \
    --set gatewayAPI.enableAppProtocol=true

# 5. Upgrade for changes to apply
talosctl upgrade-k8s --nodes <controlplane-ip> --to 1.x.y
```

## Persistent storage

We will be using the Proxmox CSI driver for this

```bash
# 1. Create role on hypervisor
pveum role add KubernetesCSI -privs "VM.Config.Disk Datastore.AllocateSpace Datastore.Audit VM.Audit"

# 2. Create the System User & Token:
pveum user add kubernetes-csi@pve
pveum user token add kubernetes-csi@pve csi-token --privsep 0

# 3. Bind Permissions to your LVM-thin Storage:
pveum aclmod / -user kubernetes-csi@pve -role KubernetesCSI

# 4. Patch worker nodes
machine:
  kubelet:
    extraMounts:
      - destination: /dev
        type: bind
        source: /dev
        options:
          - bind
          - rshared
          - rw

# 5. Deploy Proxmox CSI driver
...


```

## 🧹 Cleanup

To tear down the cluster and securely delete all associated secrets and virtual machines:
```bash
pulumi destroy
```
