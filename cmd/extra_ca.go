package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/tools/clientcmd"
)

// extraCABundlePath returns the path of an optional extra CA bundle (for
// example a corporate TLS-intercepting proxy CA) that gets appended to the
// CA of Azure clusters. It is a variable so tests can override it.
var extraCABundlePath = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cert", "combined-ca-bundle.pem")
}

// appendExtraCA appends the PEM bundle at bundlePath to the embedded
// certificate authority of the cluster used by the given kubeconfig context.
// It does nothing if bundlePath is empty or the file does not exist.
func appendExtraCA(contextName, bundlePath string) error {
	if bundlePath == "" {
		return nil
	}
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read %s: %w", bundlePath, err)
	}

	configPath := getDefaultKubeConfigPath()
	if configPath == "" {
		return fmt.Errorf("could not determine kubeconfig path")
	}

	loadingRules := clientcmd.ClientConfigLoadingRules{Precedence: []string{configPath}}
	config, err := loadingRules.Load()
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	kctx := config.Contexts[contextName]
	if kctx == nil {
		return fmt.Errorf("context %q not found in kubeconfig", contextName)
	}
	cluster := config.Clusters[kctx.Cluster]
	if cluster == nil {
		return fmt.Errorf("cluster %q not found in kubeconfig", kctx.Cluster)
	}

	if bytes.Contains(cluster.CertificateAuthorityData, bytes.TrimSpace(bundle)) {
		return nil
	}

	ca := bytes.TrimRight(cluster.CertificateAuthorityData, "\n")
	merged := append(append(ca, '\n'), bundle...)
	cluster.CertificateAuthorityData = merged
	cluster.CertificateAuthority = ""

	configAccess := clientcmd.NewDefaultPathOptions()
	configAccess.GlobalFile = configPath
	if err := clientcmd.ModifyConfig(configAccess, *config, true); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}
	return nil
}
