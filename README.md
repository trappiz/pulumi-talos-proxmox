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

# 1. Extract the kubeconfig from the Pulumi stack
pulumi stack output kubeconfig --show-secrets > kubeconfig.yaml

# 2. Point kubectl to the extracted file
export KUBECONFIG=$PWD/kubeconfig.yaml

# 3. Verify access
kubectl get nodes -o wide
```
(Note: It may take 60-90 seconds after Pulumi finishes for the Talos API and Kubernetes API to become fully responsive).
## 🧹 Cleanup

To tear down the cluster and securely delete all associated secrets and virtual machines:
```bash
pulumi destroy
```
