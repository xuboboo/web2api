package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/xuboboo/web2api/core"
	browserprofile "github.com/xuboboo/web2api/core/browser"
	"github.com/xuboboo/web2api/core/fpcheck"
	"github.com/xuboboo/web2api/core/fpcheck/detectors"
)

func newFingerprintCommand() *cobra.Command {
	var detectorName, target, selector, artifactDir, profileID, proxyURL string
	var timeout, wait time.Duration
	var headless bool
	cmd := &cobra.Command{
		Use:   "fingerprint-check",
		Short: "Run browser fingerprint diagnostics locally",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) (runErr error) {
			if timeout <= 0 || wait < 0 {
				return fmt.Errorf("probe timeout must be positive and wait must be non-negative")
			}
			selected, err := detectors.SelectWithCustomSelector(detectorName, target, selector)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			if profileID != "" {
				if _, ok := browserprofile.ProfileByID(profileID); !ok {
					return fmt.Errorf("unknown browser profile %q", profileID)
				}
				ctx = core.WithForcedProfileID(ctx, profileID)
			}
			opts := buildBrowserOptions()
			opts.IsHeadless = headless
			opts.Timeout = timeout
			opts.ProxyURL = proxyURL
			browser, err := core.NewBrowser(opts)
			if err != nil {
				return err
			}
			defer func() {
				runErr = errors.Join(runErr, wrapIfError("close diagnostic browser", browser.Close()))
			}()

			reports := make([]fpcheck.Report, 0, len(selected))
			for i, detector := range selected {
				options := fpcheck.RunOptions{ArtifactDir: artifactDir}
				if i == len(selected)-1 {
					options.WaitBeforeExtract = wait
				}
				report, err := fpcheck.RunWithOptions(ctx, browser, detector, options)
				if err != nil {
					return fmt.Errorf("%s: %w", detector.Name(), err)
				}
				reports = append(reports, report)
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(reports)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&detectorName, "detector", "all", "Detector name")
	flags.StringVar(&target, "url", "", "Target URL for the custom detector")
	flags.StringVar(&selector, "selector", "", "CSS selector for the custom detector")
	flags.StringVar(&artifactDir, "artifact-dir", "", "Optional directory for screenshots")
	flags.StringVar(&profileID, "profile", "", "Browser profile ID")
	flags.StringVar(&proxyURL, "proxy", "", "Browser proxy URL")
	flags.DurationVar(&timeout, "probe-timeout", 150*time.Second, "Total diagnostic timeout")
	flags.DurationVar(&wait, "wait", 0, "Wait before the last detector extracts results")
	flags.BoolVar(&headless, "headless", true, "Use headless Chromium")
	return cmd
}

func init() {
	RootCmd.AddCommand(newFingerprintCommand())
}
