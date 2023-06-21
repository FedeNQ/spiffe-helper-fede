package sidecar

import (
	"errors"
	"os"

	"github.com/hashicorp/hcl"
	"github.com/spiffe/go-spiffe/v2/logger"
)

// Config contains config variables when creating a SPIFFE Sidecar.
type Config struct {
	AgentAddress           string `hcl:"agent_address"`
	AgentAddressDeprecated string `hcl:"agentAddress"`
	Cmd                    string `hcl:"cmd"`
	CmdArgs                string `hcl:"cmd_args"`
	CmdArgsDeprecated      string `hcl:"cmdArgs"`
	CertDir                string `hcl:"cert_dir"`
	CertDirDeprecated      string `hcl:"certDir"`
	// Merge intermediate certificates into Bundle file instead of SVID file,
	// it is useful is some scenarios like MySQL,
	// where this is the expected format for presented certificates and bundles
	AddIntermediatesToBundle           bool   `hcl:"add_intermediates_to_bundle"`
	AddIntermediatesToBundleDeprecated bool   `hcl:"addIntermediatesToBundle"`
	SvidFileName                       string `hcl:"svid_file_name"`
	SvidFileNameDeprecated             string `hcl:"svidFileName"`
	SvidKeyFileName                    string `hcl:"svid_key_file_name"`
	SvidKeyFileNameDeprecated          string `hcl:"svidKeyFileName"`
	SvidBundleFileName                 string `hcl:"svid_bundle_file_name"`
	SvidBundleFileNameDeprecated       string `hcl:"svidBundleFileName"`
	RenewSignal                        string `hcl:"renew_signal"`
	RenewSignalDeprecated              string `hcl:"renewSignal"`
	// TODO: is there a reason for this to be exposed? and inside of config?
	ReloadExternalProcess func() error
	// TODO: is there a reason for this to be exposed? and inside of config?
	Log logger.Logger
}

type fakeLogger struct {
	logger.Logger

	Warnings []string
} // private

func (f *fakeLogger) Warnf(format string, args ...interface{}) {
	f.Warnings = append(f.Warnings, format)
}

func GetWarning(s1 string, s2 string) string {
	return s1 + " will be deprecated, should be used as " + s2
}

// ParseConfig parses the given HCL file into a SidecarConfig struct
func ParseConfig(file string) (*Config, error) {
	sidecarConfig := new(Config)

	// Read HCL file
	dat, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	// Parse HCL
	if err := hcl.Decode(sidecarConfig, string(dat)); err != nil {
		return nil, err
	}

	return sidecarConfig, nil
}

func ValidateConfig(c *Config) error {
	if err := validateOSConfig(c); err != nil {
		return err
	}

	if c.Log == nil {
		c.Log = &fakeLogger{}
	}

	if c.AgentAddressDeprecated != "" {
		if c.AgentAddress != "" {
			return errors.New("duplicated agentAddress")
		}
		c.Log.Warnf(GetWarning("agentAddress", "agent_address"))
		c.AgentAddress = c.AgentAddressDeprecated
	}

	if c.CmdArgsDeprecated != "" {
		if c.CmdArgs != "" {
			return errors.New("duplicated CmdArgs")
		}
		c.Log.Warnf(GetWarning("cmdArgs", "cmd_args"))
		c.CmdArgs = c.CmdArgsDeprecated
	}

	if c.CertDirDeprecated != "" {
		if c.CertDir != "" {
			return errors.New("duplicated CertDir")
		}
		c.Log.Warnf(GetWarning("certDir", "cert_dir"))
		c.CertDir = c.CertDirDeprecated
	}

	if c.SvidFileNameDeprecated != "" {
		if c.SvidFileName != "" {
			return errors.New("duplicated SvidFileName")
		}
		c.Log.Warnf(GetWarning("svidFileName", "svid_file_name"))
		c.SvidFileName = c.SvidFileNameDeprecated
	}

	if c.SvidKeyFileNameDeprecated != "" {
		if c.SvidKeyFileName != "" {
			return errors.New("duplicated SvidKeyFileName")
		}
		c.Log.Warnf(GetWarning("svidKeyFileName", "svid_key_file_name"))
		c.SvidKeyFileName = c.SvidKeyFileNameDeprecated
	}

	if c.SvidBundleFileNameDeprecated != "" {
		if c.SvidBundleFileName != "" {
			return errors.New("duplicated SvidBundleFileName")
		}
		c.Log.Warnf(GetWarning("svidBundleFileName", "svid_bundle_file_name"))
		c.SvidBundleFileName = c.SvidBundleFileNameDeprecated

	}

	if c.RenewSignalDeprecated != "" {
		if c.RenewSignal != "" {
			return errors.New("duplicated RenewSignal")
		}
		c.Log.Warnf(GetWarning("renewSignal", "renew_signal"))
		c.RenewSignal = c.RenewSignalDeprecated
	}

	switch {
	case c.AgentAddress == "":
		return errors.New("agentAddress is required")
	case c.SvidFileName == "":
		return errors.New("svidFileName is required")
	case c.SvidKeyFileName == "":
		return errors.New("svidKeyFileName is required")
	case c.SvidBundleFileName == "":
		return errors.New("svidBundleFileName is required")
	default:
		return nil
	}
}
