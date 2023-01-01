package commands

import (
	"context"
	"fmt"
	"path"

	"github.com/spf13/cobra"
	"github.com/wundergraph/wundergraph/pkg/operations"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/wundergraph/wundergraph/cli/helpers"
	"github.com/wundergraph/wundergraph/pkg/bundler"
	"github.com/wundergraph/wundergraph/pkg/files"
	"github.com/wundergraph/wundergraph/pkg/scriptrunner"
	"github.com/wundergraph/wundergraph/pkg/webhooks"
)

var (
	generateAndPublish bool
	offline            bool
)

// generateCmd represents the generate command
var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate the production config",
	Long: `Generate the production config to start the node and hook component
 with 'wunderctl start' or individually with 'wunderctl node start', 'wunderctl
 server start'. All files are stored to .wundergraph/generated. The local
 introspection cache has precedence. You can overwrite this behavior by passing
 --no-cache to the command`,
	Annotations: map[string]string{
		"telemetry": "true",
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		wunderGraphDir, err := files.FindWunderGraphDir(_wunderGraphDirConfig)
		if err != nil {
			return err
		}
		// only validate if the file exists
		_, err = files.CodeFilePath(wunderGraphDir, configEntryPointFilename)
		if err != nil {
			return err
		}

		// optional, no error check
		codeServerFilePath, _ := files.CodeFilePath(wunderGraphDir, serverEntryPointFilename)

		ctx := context.Background()

		configOutFile := path.Join("generated", "bundle", "config.js")

		configRunner := scriptrunner.NewScriptRunner(&scriptrunner.Config{
			Name:          "config-runner",
			Executable:    "node",
			ScriptArgs:    []string{configOutFile},
			AbsWorkingDir: wunderGraphDir,
			Logger:        log,
			ScriptEnv: append(
				helpers.CliEnv(rootFlags),
				// Run scripts in prod mode
				"NODE_ENV=production",
				"WG_THROW_ON_OPERATION_LOADING_ERROR=true",
				fmt.Sprintf("WG_ENABLE_INTROSPECTION_CACHE=%t", !disableCache),
				fmt.Sprintf("WG_ENABLE_INTROSPECTION_OFFLINE=%t", offline),
				fmt.Sprintf("WG_DIR_ABS=%s", wunderGraphDir),
				fmt.Sprintf("%s=%s", wunderctlBinaryPathEnvKey, wunderctlBinaryPath()),
			),
		})
		defer func() {
			log.Debug("Stopping config-runner")
			err := configRunner.Stop()
			if err != nil {
				log.Error("Stopping runner failed",
					zap.String("runnerName", "config-runner"),
					zap.Error(err),
				)
			}
		}()

		var onAfterBuild func() error

		if codeServerFilePath != "" {
			serverOutFile := path.Join(wunderGraphDir, "generated", "bundle", "server.js")
			webhooksOutDir := path.Join("generated", "bundle", "webhooks")
			webhooksDir := path.Join(wunderGraphDir, webhooks.WebhookDirectoryName)
			operationsDir := path.Join(wunderGraphDir, operations.DirectoryName)
			operationsOutDir := path.Join("generated", "bundle", "operations")

			var webhooksBundler *bundler.Bundler
			var operationsBundler *bundler.Bundler

			if files.DirectoryExists(webhooksDir) {
				webhookPaths, err := webhooks.GetWebhooks(wunderGraphDir)
				if err != nil {
					return err
				}
				webhooksBundler = bundler.NewBundler(bundler.Config{
					Name:          "webhooks-bundler",
					EntryPoints:   webhookPaths,
					AbsWorkingDir: wunderGraphDir,
					OutDir:        webhooksOutDir,
					Logger:        log,
					OnAfterBundle: func() error {
						log.Debug("Webhooks bundled!", zap.String("bundlerName", "webhooks-bundler"))
						return nil
					},
				})
			}

			if files.DirectoryExists(operationsDir) {
				operationsPaths, err := operations.GetPaths(wunderGraphDir)
				if err != nil {
					return err
				}
				operationsBundler = bundler.NewBundler(bundler.Config{
					Name:          "operations-bundler",
					EntryPoints:   operationsPaths,
					AbsWorkingDir: wunderGraphDir,
					OutDir:        operationsOutDir,
					Logger:        log,
					OnAfterBundle: func() error {
						log.Debug("Operations bundled!", zap.String("bundlerName", "operations-bundler"))
						return nil
					},
				})
			}

			hooksBundler := bundler.NewBundler(bundler.Config{
				Name:          "server-bundler",
				AbsWorkingDir: wunderGraphDir,
				EntryPoints:   []string{serverEntryPointFilename},
				OutFile:       serverOutFile,
				Logger:        log,
			})

			onAfterBuild = func() error {
				<-configRunner.Run(ctx)

				if !configRunner.Successful() {
					return fmt.Errorf("configuration could not be generated. Process exit with code %d",
						configRunner.ExitCode(),
					)
				}

				var wg errgroup.Group

				wg.Go(func() error {
					// bundle hooks
					return hooksBundler.Bundle()
				})

				if webhooksBundler != nil {
					wg.Go(func() error {
						// bundle webhooks
						return webhooksBundler.Bundle()
					})
				}

				if operationsBundler != nil {
					wg.Go(func() error {
						// bundle operations
						return operationsBundler.Bundle()
					})
				}

				err := wg.Wait()
				log.Debug("Config built!", zap.String("bundlerName", "config-bundler"))

				return err
			}

			err = operationsBundler.Bundle()
			if err != nil {
				return err
			}

		} else {
			log.Info("hooks EntryPoint not found, skipping", zap.String("file", serverEntryPointFilename))
			onAfterBuild = func() error {
				<-configRunner.Run(ctx)

				if !configRunner.Successful() {
					return fmt.Errorf("configuration could not be generated. Process exit with code %d",
						configRunner.ExitCode(),
					)
				}

				log.Debug("Config built!", zap.String("bundlerName", "config-bundler"))

				return nil
			}
		}

		configBundler := bundler.NewBundler(bundler.Config{
			Name:          "config-bundler",
			AbsWorkingDir: wunderGraphDir,
			EntryPoints:   []string{configEntryPointFilename},
			OutFile:       configOutFile,
			Logger:        log,
			IgnorePaths: []string{
				"generated",
				"node_modules",
			},
			OnAfterBundle: onAfterBuild,
		})

		err = configBundler.Bundle()

		return err
	},
}

func init() {
	generateCmd.Flags().BoolVarP(&generateAndPublish, "publish", "p", false, "publish the generated API immediately")
	generateCmd.Flags().BoolVar(&offline, "offline", false, "disables loading resources from the network")
	rootCmd.AddCommand(generateCmd)
}
