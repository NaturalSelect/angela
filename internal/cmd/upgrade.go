package cmd

import (
	"github.com/NaturalSelect/angela/internal/update"
	"github.com/NaturalSelect/angela/internal/version"
	"github.com/spf13/cobra"
)

func init() {
	upgradeCmd.Flags().Bool("pre-release", false, "Also consider pre-releases when looking for a newer version")
}

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade Angela to the latest release",
	Long: `Download the latest Angela release from GitHub and install it.

Before anything is installed, Angela verifies the Sigstore (cosign) signature
on the release's checksums.txt, which must come from the release workflow for
that tag, and checks the download against it. This needs access to the
Sigstore trust root at tuf-repo-cdn.sigstore.dev. A release without a valid
signature is refused.

Angela never installs an older version than the one running. A pre-release
build stays where it is until a newer release is published, and it is never
rolled back to an older stable release. Pre-releases are only offered as
upgrade targets with --pre-release.

If Angela was installed with a package manager (dpkg, rpm, apk or pacman),
the new version is installed through that package manager, using sudo when
needed. Otherwise the running executable is replaced in place.`,
	Example: `
# Upgrade to the latest stable release
angela upgrade

# Also consider pre-releases (release candidates)
angela upgrade --pre-release
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		includePrerelease, _ := cmd.Flags().GetBool("pre-release")

		upgrader, err := update.NewUpgrader(version.Version, cmd.OutOrStdout())
		if err != nil {
			return err
		}
		return upgrader.Upgrade(cmd.Context(), includePrerelease)
	},
}
