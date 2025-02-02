package sidecar

import (
	"crypto/x509"
	"encoding/csv"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync/atomic"

	"github.com/sirupsen/logrus"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	certsFileMode       = os.FileMode(0644)
	keyFileMode         = os.FileMode(0600)
	defaultAgentAddress = "/tmp/spire-agent/public/api.sock"
)

// Sidecar is the component that consumes the Workload API and renews certs
// implements the interface Sidecar
type Sidecar struct {
	config         *Config
	processRunning int32
	process        *os.Process
	certReadyChan  chan struct{}
}

// New creates a new SPIFFE sidecar
func New(configPath string, log logrus.FieldLogger) (*Sidecar, error) {
	config, err := ParseConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %q: %w", configPath, err)
	}

	if log == nil {
		log = logrus.New()
	}
	config.Log = log

	if err := ValidateConfig(config); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	if config.AgentAddress == "" {
		config.AgentAddress = os.Getenv("SPIRE_AGENT_ADDRESS")
		if config.AgentAddress == "" {
			config.AgentAddress = defaultAgentAddress
		}
	}

	config.Log.WithField("agent_address", config.AgentAddress).Info("Connecting to agent")
	if config.Cmd == "" {
		config.Log.Warn("No cmd defined to execute.")
	}

	return &Sidecar{
		config:        config,
		certReadyChan: make(chan struct{}, 1),
	}, nil
}

// CertReadyChan returns a channel to know when the certificates are ready
func (s *Sidecar) CertReadyChan() <-chan struct{} {
	return s.certReadyChan
}

// updateCertificates Updates the certificates stored in disk and signal the Process to restart
func (s *Sidecar) updateCertificates(svidResponse *workloadapi.X509Context) {
	s.config.Log.Info("Updating certificates")

	err := s.dumpBundles(svidResponse)
	if err != nil {
		s.config.Log.WithError(err).Error("Unable to dump bundle")
		return
	}

	if s.config.Cmd != "" {
		if err := s.signalProcess(); err != nil {
			s.config.Log.WithError(err).Error("Unable to signal process")
		}
	}

	select {
	case s.certReadyChan <- struct{}{}:
	default:
	}
}

// signalProcess sends the configured Renew signal to the process running the proxy
// to reload itself so that the proxy uses the new SVID
func (s *Sidecar) signalProcess() (err error) {
	// TODO: is ReloadExternalProcess still used?
	switch s.config.ReloadExternalProcess {
	case nil:
		if atomic.LoadInt32(&s.processRunning) == 0 {
			cmdArgs, err := getCmdArgs(s.config.CmdArgs)
			if err != nil {
				return fmt.Errorf("error parsing cmd arguments: %w", err)
			}

			cmd := exec.Command(s.config.Cmd, cmdArgs...) // #nosec
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			err = cmd.Start()
			if err != nil {
				return fmt.Errorf("error executing process: %v\n%w", s.config.Cmd, err)
			}
			s.process = cmd.Process
			go s.checkProcessExit()
		} else {
			if err := s.SignalProcess(); err != nil {
				return err
			}
		}

	default:
		if err = s.config.ReloadExternalProcess(); err != nil {
			return fmt.Errorf("error reloading external process: %w", err)
		}
	}

	return nil
}

func (s *Sidecar) checkProcessExit() {
	atomic.StoreInt32(&s.processRunning, 1)
	_, err := s.process.Wait()
	if err != nil {
		s.config.Log.Errorf("error waiting for process exit: %v", err)
	}

	atomic.StoreInt32(&s.processRunning, 0)
}

// dumpBundles takes a X509SVIDResponse, representing a svid message from
// the Workload API, and calls writeCerts and writeKey to write to disk
// the svid, key and bundle of certificates.
// It is possible to change output setting `addIntermediatesToBundle` as true.
func (s *Sidecar) dumpBundles(svidResponse *workloadapi.X509Context) error {
	// There may be more than one certificate, but we are interested in the first one only
	svid := svidResponse.DefaultSVID()

	svidFile := path.Join(s.config.CertDir, s.config.SvidFileName)
	svidKeyFile := path.Join(s.config.CertDir, s.config.SvidKeyFileName)
	svidBundleFile := path.Join(s.config.CertDir, s.config.SvidBundleFileName)

	certs := svid.Certificates
	bundleSet, found := svidResponse.Bundles.Get(svid.ID.TrustDomain())
	if !found {
		return fmt.Errorf("no bundles found for %s trust domain", svid.ID.TrustDomain().String())
	}
	bundles := bundleSet.X509Authorities()
	privateKey, err := x509.MarshalPKCS8PrivateKey(svid.PrivateKey)
	if err != nil {
		return err
	}

	// Add intermediates into bundles, and remove them from certs
	if s.config.AddIntermediatesToBundle {
		bundles = append(bundles, certs[1:]...)
		certs = []*x509.Certificate{certs[0]}
	}

	if err := writeCerts(svidFile, certs); err != nil {
		return err
	}

	if err := writeKey(svidKeyFile, privateKey); err != nil {
		return err
	}

	if err := writeCerts(svidBundleFile, bundles); err != nil {
		return err
	}

	return nil
}

// x509Watcher is a sample implementation of the workload.X509SVIDWatcher interface
type x509Watcher struct {
	sidecar *Sidecar
}

// OnX509ContextUpdate is run every time an SVID is updated
func (w x509Watcher) OnX509ContextUpdate(svids *workloadapi.X509Context) {
	for _, svid := range svids.SVIDs {
		w.sidecar.config.Log.WithField("spiffe_id", svid.ID).Info("Received update")
	}

	w.sidecar.updateCertificates(svids)
}

// OnX509ContextWatchError is run when the client runs into an error
func (w x509Watcher) OnX509ContextWatchError(err error) {
	if status.Code(err) != codes.Canceled {
		w.sidecar.config.Log.Errorf("Error while watching x509 context: %v", err)
	}
}

// writeCerts takes an array of certificates,
// and encodes them as PEM blocks, writing them to file
func writeCerts(file string, certs []*x509.Certificate) error {
	var pemData []byte
	for _, cert := range certs {
		b := &pem.Block{
			Type:  "CERTIFICATE",
			Bytes: cert.Raw,
		}
		pemData = append(pemData, pem.EncodeToMemory(b)...)
	}

	return os.WriteFile(file, pemData, certsFileMode)
}

// writeKey takes a private key as a slice of bytes,
// formats as PEM, and writes it to file
func writeKey(file string, data []byte) error {
	b := &pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: data,
	}

	return os.WriteFile(file, pem.EncodeToMemory(b), keyFileMode)
}

// getCmdArgs receives the command line arguments as a string
// and split it at spaces, except when the space is inside quotation marks
func getCmdArgs(args string) ([]string, error) {
	if args == "" {
		return []string{}, nil
	}

	r := csv.NewReader(strings.NewReader(args))
	r.Comma = ' ' // space
	cmdArgs, err := r.Read()
	if err != nil {
		return nil, err
	}

	return cmdArgs, nil
}
