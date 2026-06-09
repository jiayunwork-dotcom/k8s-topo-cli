package client

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

type ClusterClient struct {
	Clientset     *kubernetes.Clientset
	MetricsClient *metricsclient.Clientset
	Config        *rest.Config
	Namespace     string
	Context       string
}

func NewClusterClient(kubeconfig, context, namespace string) (*ClusterClient, error) {
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			home := homedir.HomeDir()
			if home != "" {
				kubeconfig = home + "/.kube/config"
			}
		}
	}

	config, err := buildConfig(kubeconfig, context)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	config.QPS = 10
	config.Burst = 20

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	var metricsClient *metricsclient.Clientset
	metricsClient, err = metricsclient.NewForConfig(config)
	if err != nil {
		metricsClient = nil
	}

	cc := &ClusterClient{
		Clientset:     clientset,
		MetricsClient: metricsClient,
		Config:        config,
		Namespace:     namespace,
		Context:       context,
	}

	if err := cc.validateConnection(); err != nil {
		return nil, err
	}

	return cc, nil
}

func buildConfig(kubeconfig, context string) (*rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.ExplicitPath = kubeconfig

	configOverrides := &clientcmd.ConfigOverrides{}
	if context != "" {
		configOverrides.CurrentContext = context
	}

	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
	return clientConfig.ClientConfig()
}

func (c *ClusterClient) validateConnection() error {
	_, err := c.Clientset.ServerVersion()
	if err != nil {
		return classifyConnectionError(err, c.Config)
	}
	return nil
}

func classifyConnectionError(err error, config *rest.Config) error {
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return fmt.Errorf("connection timed out: API server at %s is unreachable (network issue or firewall)", config.Host)
	}

	errMsg := err.Error()

	if strings.Contains(errMsg, "certificate") || strings.Contains(errMsg, "x509") {
		if strings.Contains(errMsg, "expired") || strings.Contains(errMsg, "not valid") {
			return fmt.Errorf("certificate expired or not yet valid: %s", extractCertDetail(errMsg))
		}
		if strings.Contains(errMsg, "unknown authority") || strings.Contains(errMsg, "verify") {
			return fmt.Errorf("certificate verification failed: the cluster CA is not trusted. Check your kubeconfig certificate-authority field")
		}
		return fmt.Errorf("certificate error: %s", errMsg)
	}

	if strings.Contains(errMsg, "Unauthorized") || strings.Contains(errMsg, "401") {
		return fmt.Errorf("authentication failed: token or credentials are invalid. Try refreshing your credentials")
	}

	if strings.Contains(errMsg, "Forbidden") || strings.Contains(errMsg, "403") {
		return fmt.Errorf("authorization failed: the current user does not have permission to access the cluster")
	}

	if strings.Contains(errMsg, "connection refused") {
		return fmt.Errorf("connection refused: API server at %s is not accepting connections. Is the cluster running?", config.Host)
	}

	if strings.Contains(errMsg, "no such host") || strings.Contains(errMsg, "lookup") {
		return fmt.Errorf("DNS resolution failed: cannot resolve API server hostname. Check your network and kubeconfig")
	}

	return fmt.Errorf("failed to connect to API server: %s", errMsg)
}

func extractCertDetail(errMsg string) string {
	parts := strings.Split(errMsg, ":")
	if len(parts) > 1 {
		return strings.TrimSpace(parts[len(parts)-1])
	}
	return errMsg
}

func (c *ClusterClient) IsMetricsAvailable() bool {
	if c.MetricsClient == nil {
		return false
	}
	_, err := c.MetricsClient.MetricsV1beta1().PodMetricses("").List(nil, metav1.ListOptions{Limit: 1})
	return err == nil
}

func CheckAPIServerReachable(host string) error {
	url := strings.TrimPrefix(host, "https://")
	url = strings.TrimPrefix(url, "http://")
	if idx := strings.Index(url, "/"); idx > 0 {
		url = url[:idx]
	}

	conn, err := net.DialTimeout("tcp", url, 5*time.Second)
	if err != nil {
		return fmt.Errorf("API server %s is not reachable: %v", host, err)
	}
	conn.Close()

	if strings.HasPrefix(host, "https://") {
		tlsConfig := &tls.Config{InsecureSkipVerify: true}
		tlsConn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", url, tlsConfig)
		if err != nil {
			return fmt.Errorf("TLS handshake failed: %v", err)
		}
		certs := tlsConn.ConnectionState().PeerCertificates
		tlsConn.Close()
		if len(certs) > 0 {
			now := time.Now()
			for _, cert := range certs {
				if now.After(cert.NotAfter) {
					return fmt.Errorf("server certificate expired on %s", cert.NotAfter.Format(time.RFC3339))
				}
				if now.Before(cert.NotBefore) {
					return fmt.Errorf("server certificate not valid until %s", cert.NotBefore.Format(time.RFC3339))
				}
			}
		}
	}

	return nil
}

func (c *ClusterClient) ProbeTLS() []*x509.Certificate {
	url := strings.TrimPrefix(c.Config.Host, "https://")
	url = strings.TrimPrefix(url, "http://")
	if idx := strings.Index(url, "/"); idx > 0 {
		url = url[:idx]
	}

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", url, tlsConfig)
	if err != nil {
		return nil
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates
}
