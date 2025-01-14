package supply

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v2"

	"github.com/cloudfoundry/libbuildpack"
)

type Manifest interface {
	AllDependencyVersions(string) []string
}

type Installer interface {
	InstallDependency(libbuildpack.Dependency, string) error
}

type Stager interface {
	BuildDir() string
	DepDir() string
	DepsDir() string
	DepsIdx() string
	LinkDirectoryInDepDir(string, string) error
}

type Command interface {
	Run(cmd *exec.Cmd) error
}

type Supplier struct {
	Stager    Stager
	Manifest  Manifest
	Installer Installer
	Command   Command
	Log       *libbuildpack.Logger
}

type Packages struct {
	Packages []Source `yaml:"packages"`
}

type Source struct {
	CranMirror string    `yaml:"cran_mirror"`
	Packages   []Package `yaml:"packages"`
	Ncpus      int       `yaml:"num_threads"`
}

type Package struct {
	Name string `yaml:"name"`
}

func New(stager Stager, command Command, manifest Manifest, installer Installer, logger *libbuildpack.Logger) *Supplier {
	return &Supplier{
		Stager:    stager,
		Command:   command,
		Manifest:  manifest,
		Installer: installer,
		Log:       logger,
	}
}

func (s *Supplier) Run() error {
	s.Log.BeginStep("Supplying R")

	if err := s.InstallR(); err != nil {
		s.Log.Error("Error installing R: %v", err)
		return err
	}

	if err := s.RewriteRHome(); err != nil {
		s.Log.Error("Error rewriting R_HOME: %v", err)
		return err
	}

	yaml := libbuildpack.NewYAML()
	path_to_ryml := filepath.Join(s.Stager.BuildDir(), "r.yml")
	packages_to_install := Packages{}
	if err := yaml.Load(path_to_ryml, &packages_to_install); err != nil {
		s.Log.Error("Couldn't load r.yml: %s", err)
		return fmt.Errorf("Couldn't load r.yml: %s", err)
	}

	if err := s.InstallPackages(packages_to_install); err != nil {
		s.Log.Error("Error installing packages: %v", err)
		return err
	}

	return nil
}

func (s *Supplier) InstallPackages(packages_to_install Packages) error {
	isAlphaOrDot := regexp.MustCompile(`^[A-Za-z0-9.]+$`).MatchString
	for _, src := range packages_to_install.Packages {
		for _, pckg := range src.Packages {
			mirror := "NULL"; 
			if isAlphaOrDot(pckg.Name) {
				mirror = "\"" + src.CranMirror + "\""
			}
			cmd := exec.Command("R", "--vanilla", "-e", 
					    fmt.Sprintf("install.packages(c(\"%s\"), repos=%s, dependencies=TRUE, Ncpus=%d)\n", pckg.Name, mirror, src.Ncpus))
			cmd.Stdout = s.Log.Output()
			cmd.Stderr = s.Log.Output()
			cmd.Dir = s.Stager.BuildDir()
			// Set DEPS_DIR because R needs it to know its R_HOME
			cmd.Env = append(os.Environ(), "DEPS_DIR="+s.Stager.DepsDir(), "RHOME="+s.Stager.DepDir())
			if err := s.Command.Run(cmd); err != nil {
				return fmt.Errorf("Error while installing packages: %s", err)
			}
		}
	}
	return nil
}

// R> .libPaths()
func (s *Supplier) RewriteRHome() error {
	path := filepath.Join(s.Stager.DepDir(), "r", "bin", "R")
	body, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}

	body = bytes.Replace(body, []byte("/usr/local/lib/R"), []byte(filepath.Join("$DEPS_DIR", s.Stager.DepsIdx(), "r")), -1)

	return ioutil.WriteFile(path, body, 0755)
}

func (s *Supplier) InstallR() error {

	config := struct {
		R struct {
			Version string `yaml:"version"`
		} `yaml:"r"`
	}{}
	buildpackYAMLPath := filepath.Join(s.Stager.BuildDir(), "buildpack.yml")

	exists, err := libbuildpack.FileExists(buildpackYAMLPath)
	if err != nil {
		return err
	}

	constraint := "x"
	if exists {
		buf, err := ioutil.ReadFile(buildpackYAMLPath)
		if err != nil {
			return err
		}

		if err := yaml.Unmarshal(buf, &config); err != nil {
			return err
		}

		constraint = config.R.Version
	}

	versions := s.Manifest.AllDependencyVersions("r")
	ver, err := libbuildpack.FindMatchingVersion(constraint, versions)
	if err != nil {
		return err
	}

	if err := s.Installer.InstallDependency(libbuildpack.Dependency{Name: "r", Version: ver}, filepath.Join(s.Stager.DepDir(), "r")); err != nil {
		return err
	}

	if err := s.Stager.LinkDirectoryInDepDir(filepath.Join(s.Stager.DepDir(), "r", "bin"), "bin"); err != nil {
		return err
	}
	return s.Stager.LinkDirectoryInDepDir(filepath.Join(s.Stager.DepDir(), "r", "lib"), "lib")
}
