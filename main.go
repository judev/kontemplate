// Copyright (C) 2016-2017  Vincent Ambo <mail@tazj.in>
//
// Kontemplate is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.

// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"./context"
	"./templater"
	"gopkg.in/alecthomas/kingpin.v2"
)

const version string = "1.7.0"

// This variable will be initialised by the Go linker during the builder
var gitHash string

var (
	app = kingpin.New("kontemplate", "simple Kubernetes resource templating")

	// Global flags
	includes   = app.Flag("include", "Resource sets to include explicitly").Short('i').Strings()
	excludes   = app.Flag("exclude", "Resource sets to exclude explicitly").Short('e').Strings()
	variables  = app.Flag("var", "Provide variables to templates explicitly").Strings()
	kubectlBin = app.Flag("kubectl", "Path to the kubectl binary (default 'kubectl')").Default("kubectl").String()
	helmBin = app.Flag("helm", "Path to the helm binary (default 'helm')").Default("helm").String()

	// Commands
	template          = app.Command("template", "Template resource sets and print them")
	templateFile      = template.Arg("file", "Cluster configuration file to use").Required().String()
	templateOutputDir = template.Flag("output", "Output directory in which to save templated files instead of printing them").Short('o').String()

	apply       = app.Command("apply", "Template resources and pass to 'kubectl apply'")
	applyFile   = apply.Arg("file", "Cluster configuration file to use").Required().String()
	applyDryRun = apply.Flag("dry-run", "Print remote operations without executing them").Default("false").Bool()

	replace     = app.Command("replace", "Template resources and pass to 'kubectl replace'")
	replaceFile = replace.Arg("file", "Cluster configuration file to use").Required().String()

	delete     = app.Command("delete", "Template resources and pass to 'kubectl delete'")
	deleteFile = delete.Arg("file", "Cluster configuration file to use").Required().String()

	create     = app.Command("create", "Template resources and pass to 'kubectl create'")
	createFile = create.Arg("file", "Cluster configuration file to use").Required().String()

	versionCmd = app.Command("version", "Show kontemplate version")
)

func main() {
	app.HelpFlag.Short('h')

	switch kingpin.MustParse(app.Parse(os.Args[1:])) {
	case template.FullCommand():
		templateCommand()

	case apply.FullCommand():
		applyCommand()

	case replace.FullCommand():
		replaceCommand()

	case delete.FullCommand():
		deleteCommand()

	case create.FullCommand():
		createCommand()

	case versionCmd.FullCommand():
		versionCommand()
	}
}

func versionCommand() {
	if gitHash == "" {
		fmt.Printf("Kontemplate version %s (git commit unknown)\n", version)
	} else {
		fmt.Printf("Kontemplate version %s (git commit: %s)\n", version, gitHash)
	}
}

func templateCommand() {
	_, resourceSets := loadContextAndResources(templateFile)

	for _, rs := range *resourceSets {
		if len(rs.Resources) == 0 {
			fmt.Fprintf(os.Stderr, "Warning: Resource set '%s' does not exist or contains no valid templates\n", rs.Name)
			continue
		}

		if *templateOutputDir != "" {
			templateIntoDirectory(templateOutputDir, rs)
		} else {
			for _, r := range rs.Resources {
				fmt.Fprintf(os.Stderr, "Rendered file %s/%s:\n", rs.Name, r.Filename)
				fmt.Println(r.Rendered)
			}
		}
	}
}

func templateIntoDirectory(outputDir *string, rs templater.RenderedResourceSet) {
	// Attempt to create the output directory if it does not
	// already exist:
	if err := os.MkdirAll(*templateOutputDir, 0775); err != nil {
		app.Fatalf("Could not create output directory: %v\n", err)
	}

	// Nested resource sets may contain slashes in their names.
	// These are replaced with dashes for the purpose of writing a
	// flat list of output files:
	setName := strings.Replace(rs.Name, "/", "-", -1)

	for _, r := range rs.Resources {
		filename := fmt.Sprintf("%s/%s-%s", *templateOutputDir, setName, r.Filename)
		fmt.Fprintf(os.Stderr, "Writing file %s\n", filename)

		file, err := os.Create(filename)
		if err != nil {
			app.Fatalf("Could not create file %s: %v\n", filename, err)
		}

		_, err = fmt.Fprintf(file, r.Rendered)
		if err != nil {
			app.Fatalf("Error writing file %s: %v\n", filename, err)
		}
	}
}

func applyCommand() {
	ctx, resources := loadContextAndResources(applyFile)

	setupHelmRepositories(ctx)

	var kubectlArgs []string
	var helmArgs []string

	if *applyDryRun {
		kubectlArgs = []string{"apply", "-f", "-", "--dry-run"}
		helmArgs = []string{"upgrade", "-i", "-f", "-", "--dry-run"}
	} else {
		kubectlArgs = []string{"apply", "-f", "-"}
		helmArgs = []string{"upgrade", "-i", "-f", "-"}
	}

	if err := applyResourcesToCluster(ctx, &kubectlArgs, &helmArgs, resources); err != nil {
		failWithError("apply", err)
	}
}

func replaceCommand() {
	ctx, resources := loadContextAndResources(replaceFile)
	setupHelmRepositories(ctx)

	args := []string{"replace", "--save-config=true", "-f", "-"}
	var helmArgs []string

	if err := applyResourcesToCluster(ctx, &args, &helmArgs, resources); err != nil {
		failWithError("replace", err)
	}
}

func deleteCommand() {
	ctx, resources := loadContextAndResources(deleteFile)
	args := []string{"delete", "-f", "-"}
	var helmArgs []string

	if err := applyResourcesToCluster(ctx, &args, &helmArgs, resources); err != nil {
		failWithError("delete", err)
	}
}

func createCommand() {
	ctx, resources := loadContextAndResources(createFile)

	setupHelmRepositories(ctx)

	args := []string{"create", "--save-config=true", "-f", "-"}
	var helmArgs []string

	if err := applyResourcesToCluster(ctx, &args, &helmArgs, resources); err != nil {
		failWithError("create", err)
	}
}

func loadContextAndResources(file *string) (*context.Context, *[]templater.RenderedResourceSet) {
	ctx, err := context.LoadContext(*file, variables)
	if err != nil {
		app.Fatalf("Error loading context: %v\n", err)
	}

	resources, err := templater.LoadAndApplyTemplates(includes, excludes, ctx)
	if err != nil {
		app.Fatalf("Error templating resource sets: %v\n", err)
	}

	return ctx, &resources
}

func setupHelmRepositories(ctx *context.Context) {
	for _, repo := range ctx.HelmRepositories {
		helm := exec.Command(*helmBin, []string{"repo", "add", repo.Name, repo.URL}...)

		helm.Stdout = os.Stdout
		helm.Stderr = os.Stderr

		if err := helm.Start(); err != nil {
			failWithError("setup helm repositories", fmt.Errorf("helm error: %v", err))
		}
		if err := helm.Wait(); err != nil {
			failWithError("setup helm repositories", err)
		}
	}

}

func applyResourcesToCluster(c *context.Context, kubectlArgs *[]string, helmArgs *[]string, resourceSets *[]templater.RenderedResourceSet) error {
	argsWithContext := append(*kubectlArgs, fmt.Sprintf("--context=%s", c.Name))
	helmArgsWithContext := append(*helmArgs, fmt.Sprintf("--kube-context=%s", c.Name))

	for _, rs := range *resourceSets {
		if len(rs.Resources) == 0 {
			fmt.Fprintf(os.Stderr, "Warning: Resource set '%s' contains no valid templates\n", rs.Name)
			continue
		}

		var kubectl *exec.Cmd

		if rs.Type == "helm" {
			helmArgs := []string{ rs.Name, rs.Chart }
			argsWithResourceSetArgs := append(append(helmArgsWithContext, helmArgs...), rs.Args...)

			kubectl = exec.Command(*helmBin, argsWithResourceSetArgs...)
		} else {
			argsWithResourceSetArgs := append(argsWithContext, rs.Args...)

			kubectl = exec.Command(*kubectlBin, argsWithResourceSetArgs...)
		}

		stdin, err := kubectl.StdinPipe()
		if err != nil {
			return fmt.Errorf("kubectl error: %v", err)
		}

		kubectl.Stdout = os.Stdout
		kubectl.Stderr = os.Stderr

		if err = kubectl.Start(); err != nil {
			return fmt.Errorf("kubectl error: %v", err)
		}

		for _, r := range rs.Resources {
			fmt.Printf("Passing file %s/%s to kubectl\n", rs.Name, r.Filename)
			fmt.Fprintln(stdin, r.Rendered)
		}
		stdin.Close()

		if err = kubectl.Wait(); err != nil {
			return err
		}
	}

	return nil
}

func failWithError(what string, err error) {
	app.Fatalf("%s error: %v\n", what, err)
}
