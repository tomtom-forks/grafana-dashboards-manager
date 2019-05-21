package poller

import (
	"github.com/bruce34/grafana-dashboards-manager/internal/config"
	"github.com/bruce34/grafana-dashboards-manager/internal/git"
	"github.com/bruce34/grafana-dashboards-manager/internal/grafana"
	"github.com/bruce34/grafana-dashboards-manager/internal/puller"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// Setup loads (and synchronise if needed) the Git repository mentioned in the
// configuration file, then creates the poller that will pull from the Git
// repository on a regular basis and push all the changes to Grafana.
// Returns an error if the poller encountered one.
func Setup(cfg *config.Config, client *grafana.Client, delRemoved bool, singleShot bool) error {
	// Load the Git repository.
	r, needsSync, err := git.NewRepository(cfg.Git)
	if err != nil {
		return err
	}

	// Synchronise the repository if needed.
	if needsSync {
		if err = r.Sync(false); err != nil {
			return err
		}
	}

	errs := make(chan error, 1)

	// In the future we may want to poll from several Git repositories, so we
	// run the poller in a go routine.
	go func() {
		if err = poller(cfg, r, client, delRemoved, singleShot); err != nil || singleShot {
			errs <- err
			return
		}
	}()

	err = <-errs
	return err
}

// poller gets the current status of the Git repository that has previously been
// loaded, and then starts an infinite loop that will pull from the Git
// remote, then, if there was any new commit, retrieve the contents of the
// modified and added files to push them to Grafana. If set by the user via
// a command-line flag, it will also check for removed files and delete the
// corresponding dashboards from Grafana. It then sleeps for the time specified
// in the configuration file, before starting its next iteration.
// Returns an error if there was an issue checking the Git repository status,
// synchronising it, reading the files' contents, filtering out ignored files,
// or discussing with the Grafana API.
func poller(
	cfg *config.Config, repo *git.Repository, client *grafana.Client,
	delRemoved bool, singleShot bool,
) (err error) {
	previousCommit, previousFilesContents, err := pollerSetup(cfg, repo); if err != nil {
		return
	}
	if singleShot {
		err = pollerRun(cfg, repo, client, delRemoved, previousCommit, previousFilesContents)
	} else {
		for {
			err = pollerRun(cfg, repo, client, delRemoved, previousCommit, previousFilesContents)

			// Sleep before the next iteration.
			time.Sleep(time.Duration(cfg.Pusher.Config.Interval) * time.Second)
		}
	}
	return
}
	// poller gets the current status of the Git repository that has previously been
	// loaded, and then starts an infinite loop that will pull from the Git
	// remote, then, if there was any new commit, retrieve the contents of the
	// modified and added files to push them to Grafana. If set by the user via
	// a command-line flag, it will also check for removed files and delete the
	// corresponding dashboards from Grafana. It then sleeps for the time specified
	// in the configuration file, before starting its next iteration.
	// Returns an error if there was an issue checking the Git repository status,
	// synchronising it, reading the files' contents, filtering out ignored files,
	// or discussing with the Grafana API.
func pollerSetup(
		cfg *config.Config, repo *git.Repository,
	) (previousCommit *object.Commit, previousFilesContents map[string][]byte, err error) {
	// Get current state of the repo.
	// This is mainly to give an initial value to variables that will see their
	// content changed with every iteration of the loop.
	latestCommit, err := repo.GetLatestCommit()
	if err != nil {
		return
	}

	filesContents, err := repo.GetFilesContentsAtCommit(latestCommit)
	if err != nil {
		return
	}

	// We'll need to know the previous commit in order to compare its hash with
	// the one from the most recent commit after we pull from the remote, se we
	// know if there was any new commit.
	previousCommit = latestCommit
	// We need to store the content of the files from the previous iteration of
	// the loop in order to manage removed files which contents won't be
	// accessible anymore.
	previousFilesContents = filesContents
	return
}

func pollerRun(
	cfg *config.Config, repo *git.Repository, client *grafana.Client,
	delRemoved bool, previousCommit *object.Commit, previousFilesContents map[string][]byte,
) (err error) {
	var latestCommit *object.Commit
var filesContents  map[string][]byte
	// Synchronise the repository (i.e. pull from remote).
	if err = repo.Sync(true); err != nil {
		return
	}

	// Retrieve the latest commit in order to compare its hash with the
	// previous one.
	latestCommit, err = repo.GetLatestCommit()
	if err != nil {
		return
	}

	// If there is at least one new commit, handle the changes it introduces.
	if previousCommit.Hash.String() != latestCommit.Hash.String() {
		logrus.WithFields(logrus.Fields{
			"previous_hash": previousCommit.Hash.String(),
			"new_hash":      latestCommit.Hash.String(),
		}).Info("New commit(s) detected")

		// Get the updated files contents.
		filesContents, err = repo.GetFilesContentsAtCommit(latestCommit)
		if err != nil {
			return err
		}

		// Get the name of the files that have been added/modified and
		// removed between the two iterations.
		modified, removed, err := repo.GetModifiedAndRemovedFiles(previousCommit, latestCommit)
		if err != nil {
			return err
		}

		// Get a map containing the latest known content of each added,
		// modified and removed file.
		mergedContents := mergeContents(modified, removed, filesContents, previousFilesContents)

		// Separate out dashboards and folders
		dashboardsModified, foldersModified := SeparateDashboardsFolders(modified)
		dashboardsRemoved, _ := SeparateDashboardsFolders(removed)

		// Load versions
		logrus.Info("Getting local dashboard versions")
		syncPath := puller.SyncPath(cfg)
		fileVersionFile, err := puller.GetDashboardsVersions(syncPath, cfg.Git.VersionsFilePrefix)
		if err != nil {
			logrus.Error("Failed to get dashboard versions from local file system")
			return err
		}
		// ensure all folders are created
		client.CreateFolders(foldersModified, mergedContents)
		// cowardly not deleting folders as they may delete all dashboards underneath them
		var grafanaVersionFile grafana.VersionFile
		_, grafanaVersionFile, err = puller.GetGrafanaFileVersion(client, cfg)
		grafana.Push(cfg, fileVersionFile, grafanaVersionFile, dashboardsModified, mergedContents, client)
		//// Push the contents of the files that were added or modified to the
		//// Grafana API.
		//grafana.PushFiles(modified, mergedContents, fileVersionFile, client)

		// If the user requested it, delete all dashboards that were removed
		// from the repository.
		if delRemoved {
			grafana.DeleteDashboards(dashboardsRemoved, mergedContents, client)
		}

		// Grafana will auto-update the version number after we pushed the new
		// dashboards, so we use the puller mechanic to pull the updated numbers and
		// commit them in the git repo.
		if !cfg.Git.DontPush {
			if err = puller.PullGrafanaAndCommit(client, cfg); err != nil {
				logrus.WithFields(logrus.Fields{
					"error":      err,
					"repo":       cfg.Git.User + "@" + cfg.Git.URL,
					"clone_path": cfg.Git.ClonePath,
				}).Error("Call to puller returned an error")
			}
		} else {
			logrus.Info("Skipping git push - asked not to")
		}
	}

	// Update the commit and files contents to prepare for the next iteration.
	previousCommit = latestCommit
	previousFilesContents = filesContents

	return
}

// mergeContents will take as arguments a list of names of files that have been
// added/modified, a list of names of files that have been removed from the Git
// repository, the current contents of the files in the Git repository, and the
// contents of the files in the Git repository as they were at the previous
// iteration of the poller's loop.
// It will create and return a map containing the current content of all
// added/modified file, and the previous content of all removed file (since
// they are no longer accessible on disk). All files in this map is either added,
// modified or removed on the Git repository.
func mergeContents(
	modified []string, removed []string,
	filesContents map[string][]byte, previousFilesContents map[string][]byte,
) (merged map[string][]byte) {
	merged = make(map[string][]byte)

	// Load the added/modified files' contents
	for _, modifiedFile := range modified {
		merged[modifiedFile] = filesContents[modifiedFile]
	}

	// Load the removed files' contents
	for _, removedFile := range removed {
		merged[removedFile] = previousFilesContents[removedFile]
	}

	return
}

func SeparateDashboardsFolders(modified []string) (dashboardsModified []string, foldersModified []string){
	foldersModified = make([]string, 0)
	dashboardsModified = make([]string, 0)
	for _, o := range modified {
		if strings.HasPrefix(o, "dashboards") {
			dashboardsModified = append(dashboardsModified, o)
		} else if strings.HasPrefix(o, "folders") {
			foldersModified = append(foldersModified, o)
		} else {
			logrus.WithFields(logrus.Fields{
				"filename":      o,
			}).Info("Ignoring unknown changed file")
		}
	}
	return
}
