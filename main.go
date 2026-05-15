package main

import (
    "fmt"
    "os"

    "github.com/muhlba91/pulumi-proxmoxve/sdk/v6/go/proxmoxve/storage"
    "github.com/muhlba91/pulumi-proxmoxve/sdk/v6/go/proxmoxve/vm"
    "github.com/pulumi/pulumi/sdk/v3/go/pulumi"
    "github.com/pulumiverse/pulumi-talos/sdk/go/talos/cluster"
    "github.com/pulumiverse/pulumi-talos/sdk/go/talos/machine"
    "gopkg.in/yaml.v3"
)

type Config struct {
    ClusterName string       `yaml:"cluster_name"`
    PveNode     string       `yaml:"proxmox_node"`
    IsoFileId   string       `yaml:"iso_file_id"`
    Nodes       []NodeConfig `yaml:"nodes"`
}

type NodeConfig struct {
    Name        string `yaml:"name"`
    Role        string `yaml:"role"`
    IP          string `yaml:"ip"`
    Cidr        int    `yaml:"cidr"`
    Gateway     string `yaml:"gateway"`
    Dns         string `yaml:"dns"`
    Vlan        int    `yaml:"vlan"`
    Cpu         int    `yaml:"cpu"`
    Memory      int    `yaml:"memory"`
    Datastore   string `yaml:"datastore"`
    DiskSize    int    `yaml:"disksize"`
}

func main() {
    pulumi.Run(func(ctx *pulumi.Context) error {
        // Read YAML
        yamlFile, err := os.ReadFile("nodes.yaml")
        if err != nil {
            return err
        }
        var cfg Config
        err = yaml.Unmarshal(yamlFile, &cfg)
        if err != nil {
            return err
        }

        // Generate Talos Secrets
        secrets, err := machine.NewSecrets(ctx, "talos-secrets", nil)
        if err != nil {
            return err
        }

        var firstCpIP string
        for _, n := range cfg.Nodes {
            if n.Role == "controlplane" {
                firstCpIP = n.IP
                break
            }
        }
        clusterEndpoint := "https://" + firstCpIP + ":6443"

        // Iterate over nodes
        for _, n := range cfg.Nodes {

            // --- PERMANENT STATIC IP PATCH ---
            // Explicitly tell Talos to write the static IP to its disk config
            // to survive reboots
            networkPatch := fmt.Sprintf(`machine:
  network:
    interfaces:
      - interface: ens18
        dhcp: false
        addresses:
          - %s/%d
        routes:
          - network: 0.0.0.0/0
            gateway: %s
    nameservers:
      - %s`, n.IP, n.Cidr, n.Gateway, n.Dns)

            // --- STATIC HOSTNAME ---
            // Hostname must be set this way instead of through network config
            hostnamePatch := fmt.Sprintf(`
apiVersion: v1alpha1
kind: HostnameConfig
hostname: %s
auto: off`, n.Name)

            // Generate Machine Config
            machineConf := machine.GetConfigurationOutput(ctx, machine.GetConfigurationOutputArgs{
                ClusterName:     pulumi.String(cfg.ClusterName),
                MachineType:     pulumi.String(n.Role),
                ClusterEndpoint: pulumi.String(clusterEndpoint),
                MachineSecrets:  secrets.MachineSecrets,
                ConfigPatches: pulumi.StringArray{
                    pulumi.String(networkPatch),
                    pulumi.String(hostnamePatch),
                },
            })

            // --- UPLOAD CONFIG AS PROXMOX SNIPPET ---
            // This saves the Talos YAML directly into Proxmox storage
            snippetName := fmt.Sprintf("%s-userdata.yaml", n.Name)
            snippet, err := storage.NewFile(ctx, "snippet-"+n.Name, &storage.FileArgs{
                NodeName:    pulumi.String(cfg.PveNode),
                DatastoreId: pulumi.String("local"), // Must have 'Snippets' enabled in UI
                ContentType: pulumi.String("snippets"),
                SourceRaw: &storage.FileSourceRawArgs{
                    Data:     machineConf.MachineConfiguration(),
                    FileName: pulumi.String(snippetName),
                },
            })
            if err != nil {
                return err
            }

            // --- PROXMOX VM PROVISIONING ---
            nodeVM, err := vm.NewVirtualMachine(ctx, n.Name, &vm.VirtualMachineArgs{
                NodeName: pulumi.String(cfg.PveNode),
                Name:     pulumi.String(n.Name),
                Cpu: &vm.VirtualMachineCpuArgs{
                    Cores: pulumi.Int(n.Cpu),
                    Type:  pulumi.String("host"),
                },
                Memory: &vm.VirtualMachineMemoryArgs{
                    Dedicated: pulumi.Int(n.Memory),
                },
                Disks: vm.VirtualMachineDiskArray{
                     &vm.VirtualMachineDiskArgs{
                           DatastoreId: pulumi.String(n.Datastore),
                           FileFormat:  pulumi.String("raw"),
                           Interface:   pulumi.String("scsi0"),
                           Size:        pulumi.Int(n.DiskSize),
                     },
                },
                NetworkDevices: vm.VirtualMachineNetworkDeviceArray{
                    &vm.VirtualMachineNetworkDeviceArgs{
                        Bridge: pulumi.String("vmbr0"),
                        VlanId: pulumi.Int(n.Vlan),
                    },
                },
                Cdrom: &vm.VirtualMachineCdromArgs{
                    Enabled: pulumi.Bool(true),
                    FileId:  pulumi.String(cfg.IsoFileId),
                },
                // Enable the QEMU Guest Agent baked into image (image factory)
                Agent: &vm.VirtualMachineAgentArgs{
                    Enabled: pulumi.Bool(true),
                },
                // Attach the Cloud-Init Drive using the Snippet
                Initialization: &vm.VirtualMachineInitializationArgs{
                    DatastoreId:    pulumi.String("local-lvm"), // Where the CloudInit drive is stored
                    UserDataFileId: snippet.ID(),               // Passes the Talos config
                                        
                    // --- PROXMOX CLOUD-INIT NETWORK CONFIGURATION ---
                    // This populates the Proxmox UI and passes the network data via Cloud-Init
                    IpConfigs: vm.VirtualMachineInitializationIpConfigArray{
                        &vm.VirtualMachineInitializationIpConfigArgs{
                            Ipv4: &vm.VirtualMachineInitializationIpConfigIpv4Args{
                                // Proxmox expects IP and CIDR combined (e.g. "192.168.1.10/24")
                                Address: pulumi.String(fmt.Sprintf("%s/%d", n.IP, n.Cidr)),
                                Gateway: pulumi.String(n.Gateway),
                            },
                        },
                    },
                    Dns: &vm.VirtualMachineInitializationDnsArgs{
                        Servers: pulumi.StringArray{
                            pulumi.String(n.Dns),
                        },
                    },
                },
            })
            if err != nil {
                return err
            }

            // Bootstrap ONLY the first control plane node
            if n.Role == "controlplane" && n.IP == firstCpIP {
                _, err = machine.NewBootstrap(ctx, "talos-bootstrap", &machine.BootstrapArgs{
                    ClientConfiguration: secrets.ClientConfiguration,
                    Node:                pulumi.String(n.IP),
                }, pulumi.DependsOn([]pulumi.Resource{nodeVM}))
                if err != nil {
                    return err
                }
            }
        }

        // Export Kubeconfig
        kubeconfig, err := cluster.NewKubeconfig(ctx, "talos-kubeconfig", &cluster.KubeconfigArgs{
            ClientConfiguration: &cluster.KubeconfigClientConfigurationArgs{
                CaCertificate:     secrets.ClientConfiguration.CaCertificate(),
                ClientCertificate: secrets.ClientConfiguration.ClientCertificate(),
                ClientKey:         secrets.ClientConfiguration.ClientKey(),
            },
            Node: pulumi.String(firstCpIP),
        })
        if err != nil {
            return err
        }

        ctx.Export("kubeconfig", pulumi.ToSecret(kubeconfig.KubeconfigRaw))
        return nil
    })
}
