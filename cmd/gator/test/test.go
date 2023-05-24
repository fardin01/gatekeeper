package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/open-policy-agent/frameworks/constraint/pkg/instrumentation"
	cmdutils "github.com/open-policy-agent/gatekeeper/v3/cmd/gator/util"
	"github.com/open-policy-agent/gatekeeper/v3/pkg/gator/reader"
	"github.com/open-policy-agent/gatekeeper/v3/pkg/gator/test"
	"github.com/open-policy-agent/gatekeeper/v3/pkg/util"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	examples = `# test a manifest containing Kubernetes objects, Constraint Templates, and Constraints
gator test --filename="manifest.yaml"

# test a directory
gator test --filename="config-and-policies/"

# Use multiple inputs
gator test --filename="manifest.yaml" --filename="templates-and-constraints/"

# Receive input from stdin
cat manifest.yaml | gator test

# Output structured violations data
gator test --filename="manifest.yaml" --output=json

Note: The alpha "gator test" has been renamed to "gator verify".  "gator
verify" verifies individual Constraint Templates against suites of tests, where "gator
test" evaluates sets of resources against sets of Constraints and Templates.`
)

var Cmd = &cobra.Command{
	Use:     "test",
	Short:   "test evaluates resources against policies as defined by constraint templates and constraints. Note: The alpha `gator test` has been renamed to `gator verify`.",
	Example: examples,
	Run:     run,
	Args:    cobra.NoArgs,
}

var (
	flagFilenames             []string
	flagOutput                string
	flagIncludeTrace          bool
	flagGatherStats           bool
	flagEnableExternalData    bool
	flagExternalDataProviders []string
	flagImages                []string
	flagTempDir               string
	flagEnableK8sCel          bool
)

const (
	flagNameFilename = "filename"
	flagNameOutput   = "output"
	flagNameImage    = "image"
	flagNameTempDir  = "tempdir"

	stringJSON          = "json"
	stringYAML          = "yaml"
	stringHumanFriendly = "default"

	fourSpaceTab = "    "
)

func init() {
	Cmd.Flags().StringArrayVarP(&flagFilenames, flagNameFilename, "f", []string{}, "a file or directory containing Kubernetes resources.  Can be specified multiple times.")
	Cmd.Flags().StringVarP(&flagOutput, flagNameOutput, "o", "", fmt.Sprintf("Output format.  One of: %s|%s.", stringJSON, stringYAML))
	Cmd.Flags().BoolVarP(&flagIncludeTrace, "trace", "t", false, "include a trace for the underlying Constraint Framework evaluation.")
	Cmd.Flags().BoolVarP(&flagGatherStats, "stats", "", false, "include performance stats returned from the Constraint Framework.")
	Cmd.Flags().BoolVarP(&flagEnableK8sCel, "experimental-enable-k8s-native-validation", "", false, "PROTOTYPE (not stable): enable the validating admission policy driver")
	Cmd.Flags().BoolVarP(&flagEnableExternalData, "enable-external-data", "", false, "explicit flag to enable external_data() function. Defaults to false.")
	Cmd.Flags().StringArrayVarP(&flagExternalDataProviders, "external-data-providers", "", []string{}, "a file or directory containing External Data Provider manifests. Can be specified multiple times. Any External Data Provider manifests in --filename directory will be ignored.")
	Cmd.Flags().StringArrayVarP(&flagImages, flagNameImage, "i", []string{}, "a URL to an OCI image containing policies. Can be specified multiple times.")
	Cmd.Flags().StringVarP(&flagTempDir, flagNameTempDir, "d", "", fmt.Sprintf("Specifies the temporary directory to download and unpack images to, if using the --%s flag. Optional.", flagNameImage))
}

func run(cmd *cobra.Command, args []string) {
	unstrucs, err := reader.ReadSources(flagFilenames, flagImages, flagTempDir, flagExternalDataProviders)
	if err != nil {
		cmdutils.ErrFatalf("reading: %v", err)
	}
	if len(unstrucs) == 0 {
		cmdutils.ErrFatalf("no input data identified")
	}

	responses, err := test.Test(unstrucs, test.Opts{IncludeTrace: flagIncludeTrace, GatherStats: flagGatherStats, UseK8sCEL: flagEnableK8sCel, EnableExternalData: flagEnableExternalData})
	if err != nil {
		cmdutils.ErrFatalf("auditing objects: %v", err)
	}
	results := responses.Results()

	fmt.Print(formatOutput(flagOutput, results, responses.StatsEntries))

	// Whether or not we return non-zero depends on whether we have a `deny`
	// enforcementAction on one of the violated constraints
	exitCode := 0
	if enforceableFailure(results) {
		exitCode = 1
	}
	os.Exit(exitCode)
}

func formatOutput(flagOutput string, results []*test.GatorResult, stats []*instrumentation.StatsEntry) string {
	switch strings.ToLower(flagOutput) {
	case stringJSON:
		var jsonB []byte
		var err error

		if stats != nil {
			statsAndResults := map[string]interface{}{"results": results, "stats": stats}
			jsonB, err = json.MarshalIndent(statsAndResults, "", fourSpaceTab)
			if err != nil {
				cmdutils.ErrFatalf("marshaling validation json results and stats: %v", err)
			}
		} else {
			jsonB, err = json.MarshalIndent(results, "", fourSpaceTab)
			if err != nil {
				cmdutils.ErrFatalf("marshaling validation json results: %v", err)
			}
		}

		return string(jsonB)
	case stringYAML:
		yamlResults := test.GetYamlFriendlyResults(results)
		var yamlb []byte

		if stats != nil {
			statsAndResults := map[string]interface{}{"results": yamlResults, "stats": stats}

			statsJSONB, err := json.Marshal(statsAndResults)
			if err != nil {
				cmdutils.ErrFatalf("pre-marshaling stats to json: %v", err)
			}

			statsAndResultsUnmarshalled := struct {
				Results []*test.YamlGatorResult
				Stats   []*instrumentation.StatsEntry
			}{}

			err = json.Unmarshal(statsJSONB, &statsAndResultsUnmarshalled)
			if err != nil {
				cmdutils.ErrFatalf("pre-unmarshaling stats from json: %v", err)
			}

			yamlb, err = yaml.Marshal(statsAndResultsUnmarshalled)
			if err != nil {
				cmdutils.ErrFatalf("marshaling validation yaml results and stats: %v", err)
			}
		} else {
			jsonb, err := json.Marshal(yamlResults)
			if err != nil {
				cmdutils.ErrFatalf("pre-marshaling results to json: %v", err)
			}

			unmarshalled := []*test.YamlGatorResult{}
			err = json.Unmarshal(jsonb, &unmarshalled)
			if err != nil {
				cmdutils.ErrFatalf("pre-unmarshaling results from json: %v", err)
			}

			yamlb, err = yaml.Marshal(unmarshalled)
			if err != nil {
				cmdutils.ErrFatalf("marshaling validation yaml results: %v", err)
			}
		}

		return string(yamlb)
	case stringHumanFriendly:
	default:
		var buf bytes.Buffer
		if len(results) > 0 {
			for _, result := range results {
				buf.WriteString(fmt.Sprintf("[%q] Message: %q\n", result.Constraint.GetName(), result.Msg))

				if result.Trace != nil {
					buf.WriteString(fmt.Sprintf("Trace: %v", *result.Trace))
				}
			}
		}
		return buf.String()
	}

	return ""
}

func enforceableFailure(results []*test.GatorResult) bool {
	for _, result := range results {
		if result.EnforcementAction == string(util.Deny) {
			return true
		}
	}

	return false
}
