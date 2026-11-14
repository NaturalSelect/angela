package cmd

import (
	"github.com/NaturalSelect/angela/internal/update"
	"github.com/NaturalSelect/angela/internal/version"
	"github.com/spf13/cobra"
)

func init() {
	upgradeCmd.Flags().Bool("pre-release", false, "Also consider pre-releases when looking for a newer version")
	upgradeCmd.Flags().Bool("atom", false, "Read releases from the GitHub Atom feed instead of the GitHub API")
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

GitHub limits anonymous API requests per IP address. If the release list fails
with a rate limit error, set GITHUB_TOKEN or GH_TOKEN to a GitHub token to
authenticate the request. The token is only sent to api.github.com.

Alternatively, pass --atom to read the releases from the repository's Atom feed
and release pages on github.com, which the API rate limit does not apply to.
The feed only lists the most recent releases, and this relies on the layout of
GitHub's release pages, so it is a fallback rather than the default.

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

# Avoid the GitHub API, for example when it is rate limited
angela upgrade --atom
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		includePrerelease, _ := cmd.Flags().GetBool("pre-release")
		useAtom, _ := cmd.Flags().GetBool("atom")

		upgrader, err := update.NewUpgrader(version.Version, cmd.OutOrStdout())
		if err != nil {
			return err
		}
		if useAtom {
			upgrader.Source = update.AtomSource
		}
		return upgrader.Upgrade(cmd.Context(), includePrerelease)
	},
}
