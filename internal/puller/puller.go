package puller

import (
	"bytes"
	"encoding/json"
	"github.com/tidwall/sjson"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bruce34/grafana-dashboards-manager/internal/config"
	"github.com/bruce34/grafana-dashboards-manager/internal/git"
	"github.com/bruce34/grafana-dashboards-manager/internal/grafana"

	"github.com/icza/dyno"
	"github.com/sirupsen/logrus"
	gogit "gopkg.in/src-d/go-git.v4"
)

// diffVersion represents a dashboard version diff.
type diffVersion struct {
	old int
	new int
}

func SyncPath(cfg *config.Config) (syncPath string) {
	if cfg.Git != nil {
		syncPath = cfg.Git.ClonePath
	} else {
		syncPath = cfg.SimpleSync.SyncPath
	}
	return
}

func GetDashboardDefinitionsFromLocalGrafana(client *grafana.Client, cfg *config.Config, defs *grafana.DefsFile) (dashURIs []string, err error) {
	// Get URIs for all known dashboards
	logrus.Info("Getting dashboard URIs")
	dashboardMetaBySlug, foldersMetaByUID, _, err := client.GetDashboardsURIs()
	if err != nil {
		return
	}

	defs.DashboardMetaBySlug = dashboardMetaBySlug
	defs.DashboardBySlug = make(map[string]*grafana.Dashboard, 0)
	defs.FoldersMetaByUID = foldersMetaByUID
	defs.DashboardVersionByUID = make(map[string]int, 0)

	// Iterate over the dashboards URIs
	for slug, db := range dashboardMetaBySlug {
		uri := "uid/" + db.UID
		logrus.WithFields(logrus.Fields{
			"uri": uri,
		}).Debug("Retrieving dashboard")

		// Retrieve the dashboard JSON
		var dashboard *grafana.Dashboard
		dashboard, err = client.GetDashboard(uri)
		if err != nil {
			return
		}

		if len(cfg.Grafana.IgnorePrefix) > 0 {
			if strings.HasPrefix(dashboard.Name, cfg.Grafana.IgnorePrefix) {
				logrus.WithFields(logrus.Fields{
					"uri":    uri,
					"name":   dashboard.Name,
					"prefix": cfg.Grafana.IgnorePrefix,
				}).Info("Dashboard name starts with specified prefix, skipping")

				continue
			}
		}
		defs.DashboardBySlug[slug] = dashboard
		defs.DashboardVersionByUID[dashboard.UID] = dashboard.Version
	}
	return
}
func GetLibraryDefinitionsFromLocalGrafana(client *grafana.Client, cfg *config.Config, defs *grafana.DefsFile) (err error) {
	var libs []grafana.LibraryElementResponse
	var raw []json.RawMessage
	defs.LibraryMetaByUID = make(map[string]grafana.LibraryElementResponse, 0)
	defs.LibraryByUID = make(map[string]*grafana.Library, 0)
	defs.LibraryVersionByUID = make(map[string]int, 0)

	libs, raw, err = client.GetLibraryList()
	if err != nil {
		return
	}
	for i, lib := range libs {
		rawJson, _ := sjson.Delete(string(raw[i]), "model.libraryPanel.version")
		rawJson, _ = sjson.Delete(rawJson, "model.libraryPanel.created")
		rawJson, _ = sjson.Delete(rawJson, "model.libraryPanel.createdBy")
		rawJson, _ = sjson.Delete(rawJson, "model.libraryPanel.updated")
		rawJson, _ = sjson.Delete(rawJson, "model.libraryPanel.updatedBy")
		rawJson, _ = sjson.Delete(rawJson, "meta.created")
		rawJson, _ = sjson.Delete(rawJson, "meta.updated")
		rawJson, _ = sjson.Delete(rawJson, "version")
		rawJson, _ = sjson.Delete(rawJson, "folderId")
		defs.LibraryByUID[lib.Uid] = &grafana.Library{
			RawJSON: []byte(rawJson),
			Name:    lib.Name,
			Slug:    grafana.GetSluglikeName(lib.Uid, lib.Name),
			Version: lib.Version,
		}
		defs.LibraryVersionByUID[lib.Uid] = lib.Version
		defs.LibraryMetaByUID[lib.Uid] = lib
	}
	return
}

// GetDefinitionsFromGrafanaAPI gets all the dashboards and libraries from the Grafana API
func GetDefinitionsFromGrafanaAPI(client *grafana.Client, cfg *config.Config) (dashURIs []string, defs grafana.DefsFile, err error) {

	defs = grafana.DefsFile{}
	dashURIs, err = GetDashboardDefinitionsFromLocalGrafana(client, cfg, &defs)
	if err != nil {
		return
	}
	err = GetLibraryDefinitionsFromLocalGrafana(client, cfg, &defs)
	return
}

// PullGrafanaAndCommit pulls all the dashboards from Grafana except the ones
// which name starts with "test", then commits each of them to Git except for
// those that have a newer or equal version number already versioned in the
// repo.
func PullGrafanaAndCommit(client *grafana.Client, cfg *config.Config) (err error) {
	var repo *git.Repository
	var w *gogit.Worktree

	syncPath := SyncPath(cfg)
	// Only do Git stuff if there's a configuration for that. On "simple sync"
	// mode, we don't need to do any versioning.
	// We need to set syncPath accordingly, though, because we use it later.
	if cfg.Git != nil {
		// Clone or pull the repo
		repo, _, err = git.NewRepository(cfg.Git)
		if err != nil {
			return err
		}

		if err = repo.Sync(false); err != nil {
			return err
		}

		w, err = repo.Repo.Worktree()
		if err != nil {
			return err
		}
	}

	logrus.Info("PullGrafanaAndCommit: Getting dashboard versions from Grafana API")
	var APIDefs grafana.DefsFile
	_, APIDefs, err = GetDefinitionsFromGrafanaAPI(client, cfg)
	if err != nil {
		return err
	}

	dv := make(map[string]diffVersion)
	// Load versions
	logrus.Info("PullGrafanaAndCommit: Getting dashboard versions from disc/repo")
	fileDefs, oldSlugs, err := GetDefinitionsFromDisc(syncPath, cfg.Git.VersionsFilePrefix)
	if err != nil {
		return err
	}

	// Iterate over the dashboards URIs from the grafana instance
	for slug, dashboard := range APIDefs.DashboardBySlug {
		// Check if there's a version for this dashboard in the data loaded from
		// the "versions.json" file. If there's a version and it's older (lower
		// version number) than the version we just retrieved from the Grafana
		// API, or if there's no known version (ok will be false), write the
		// changes in the repo and add the modified file to the git index.
		fileVersion, ok := fileDefs.DashboardVersionByUID[dashboard.UID]
		if !ok || dashboard.Version > fileVersion {
			logrus.WithFields(logrus.Fields{
				"slug":         slug,
				"name":         dashboard.Name,
				"file_version": fileVersion,
				"new_version":  dashboard.Version,
				"uid":          dashboard.UID,
			}).Info("Grafana has a newer dashboard version than previously, updating")

			if err = addDashboardChangesToRepo(
				dashboard, syncPath, w, APIDefs.DashboardMetaBySlug[slug].FolderUID,
			); err != nil {
				return err
			}

			// We don't need to check for the value of ok because if ok is false
			// version will be initialised to the 0-value of the int type, which
			// is 0, so the previous version number will be considered to be 0,
			// which is the behaviour we want.
			dv[slug] = diffVersion{
				old: fileVersion,
				new: APIDefs.DashboardBySlug[slug].Version,
			}
		}
	}

	// remove any dashboards that have gone
	for slug, dashboard := range fileDefs.DashboardMetaBySlug {
		logrus.WithFields(logrus.Fields{
			"slug": slug,
			"name": dashboard.Title,
			"got":  APIDefs.DashboardMetaBySlug[slug],
		}).Debug("dashboard on filesystem")
		if _, ok := APIDefs.DashboardMetaBySlug[slug]; !ok {
			logrus.WithFields(logrus.Fields{
				"slug": slug,
				"name": dashboard.Title,
			}).Info("Removing dashboard from filesystem")
			removeDashboardFromFilesystem(slug, w)
		}
	}
	for _, slug := range oldSlugs {
		logrus.WithFields(logrus.Fields{
			"slug": slug,
			"got":  APIDefs.DashboardMetaBySlug[slug],
		}).Debug("old dashboard on filesystem")
		if _, ok := APIDefs.DashboardMetaBySlug[slug]; !ok {
			logrus.WithFields(logrus.Fields{
				"slug": slug,
			}).Info("Removing old dashboard from filesystem")
			removeDashboardFromFilesystem(slug, w)
		}
	}

	lv := make(map[string]diffVersion)
	// Iterate over the library-elements
	for uid, library := range APIDefs.LibraryByUID {
		// Check if there's a version for this library in the data loaded from
		// the "versions.json" file. If there's a version, and it's older (lower
		// version number) than the version we just retrieved from the Grafana
		// API, or if there's no known version (ok will be false), write the
		// changes in the repo and add the modified file to the git index.
		fileVersion, ok := fileDefs.LibraryVersionByUID[uid]
		if !ok || library.Version > fileVersion {
			logrus.WithFields(logrus.Fields{
				"name":         library.Name,
				"file_version": fileVersion,
				"new_version":  library.Version,
				"uid":          uid,
			}).Info("Grafana has a newer library-element version than previously, updating")
			if err = addLibraryChangesToRepo(
				library, syncPath, w, APIDefs.LibraryMetaByUID[uid].Meta.FolderUid); err != nil {
				return err
			}

			// We don't need to check for the value of ok because if ok is false
			// version will be initialised to the 0-value of the int type, which
			// is 0, so the previous version number will be considered to be 0,
			// which is the behaviour we want.
			lv[uid] = diffVersion{
				old: fileVersion,
				new: APIDefs.LibraryByUID[uid].Version,
			}
		}
	}

	// remove any libraries that have gone
	for uid, lib := range fileDefs.LibraryByUID {
		logrus.WithFields(logrus.Fields{
			"uid":  uid,
			"name": lib.Name,
			"got":  APIDefs.LibraryByUID[uid],
		}).Debug("dashboard on filesystem")
		if _, ok := APIDefs.LibraryByUID[uid]; !ok {
			logrus.WithFields(logrus.Fields{
				"uid":  uid,
				"name": lib.Name,
			}).Info("Removing dashboard from filesystem")
			removeLibraryFromFilesystem(lib.Slug, w)
		}
	}

	// Iterate over the folders
	for _, folderResponse := range APIDefs.FoldersMetaByUID {
		if err = addFolderChangesToRepo(folderResponse, syncPath, w); err != nil {
			return err
		}
	}

	logrus.WithFields(logrus.Fields{
		"APIDefs": APIDefs,
	}).Debug("GrafanaVersionsFile")

	logrus.WithFields(logrus.Fields{
		"fileDefs": fileDefs,
	}).Debug("FileVersionsFile")

	// Only do Git stuff if there's a configuration for that. On "simple sync"
	// mode, we don't need to do any versioning.
	if cfg.Git != nil {
		// inefficiently, we write the versions here just in case the versions are different but no dashboards are.
		// then the file will be rewritten inside commitNewVersions

		if err = writeVersions(APIDefs, dv, cfg.Git.ClonePath, cfg.Git.VersionsFilePrefix); err != nil {
			logrus.WithFields(logrus.Fields{
				"err": err,
			}).Info("Marshall error for versions file")
		}

		var status gogit.Status
		status, err = w.Status()
		if err != nil {
			return err
		}

		// Check if there's uncommited changes, and if that's the case, commit
		// them.
		if !cfg.Git.DontCommit {
			if !status.IsClean() {
				logrus.Info("Committing changes")

				if err = commitNewVersions(APIDefs, dv, w, cfg); err != nil {
					return err
				}
			}
		} else {
			logrus.Info("Skipping git commit - asked not to")
		}

		if !cfg.Git.DontPush && !cfg.Git.DontCommit {
			// Push the changes (we don't do it in the if clause above in case there
			// are pending commits in the local repo that haven't been pushed yet).
			if err = repo.Push(); err != nil {
				logrus.WithFields(logrus.Fields{
					"err": err}).Info("Failed to push")
				return err
			}
		} else {
			logrus.Info("Skipping git commit/push - asked not to")
		}
	} else {
		// If we're on simple sync mode, write versions and don't do anything
		// else.
		if err = writeVersions(APIDefs, dv, syncPath, cfg.Git.VersionsFilePrefix); err != nil {
			return err
		}
	}

	return nil
}

func addFolderChangesToRepo(
	folderResponse grafana.DbSearchResponse, clonePath string, worktree *gogit.Worktree,
) (err error) {
	folder := grafana.Folder{
		Title:     folderResponse.Title,
		UID:       folderResponse.UID,
		FolderUID: folderResponse.FolderUID,
		URI:       folderResponse.URI,
		Starred:   folderResponse.Starred,
		Tags:      folderResponse.Tags,
	}

	slugExt := folder.Title + ".json"
	dirPath := filepath.Join(clonePath, "folders")
	os.MkdirAll(dirPath, os.ModePerm)
	rawJSON, err := json.Marshal(folder)
	if err != nil {
		return
	}

	if err = rewriteFile(filepath.Join(dirPath, slugExt), rawJSON); err != nil {
		return
	}

	// If worktree is nil, it means that it hasn't been initialised, which means
	// the sync mode is "simple sync" and not Git.
	if worktree != nil {
		if _, err = worktree.Add(filepath.Join("folders", slugExt)); err != nil {
			return err
		}
	}

	return
}

// addDashboardChangesToRepo writes a dashboard content in a file, then adds the
// file to the git index, so it can be committed afterwards.
// Returns an error if there was an issue with either of the steps.
func addDashboardChangesToRepo(
	dashboard *grafana.Dashboard, clonePath string, worktree *gogit.Worktree, folderUID string) error {
	slug := grafana.GetSluglikeName(dashboard.UID, dashboard.Name)
	slugExt := slug + ".json"
	// we take out the versions here, as versions are generated by grafana and
	// therefore can't be sanely sync'd across multiple grafana instances
	var jsRaw interface{}
	if err := json.Unmarshal([]byte(dashboard.RawJSON), &jsRaw); err != nil {
		return err
	}
	// the following keys are unique only to an individual grafana instance
	dyno.Delete(jsRaw, "version")
	dyno.Delete(jsRaw, "id")
	dyno.Set(jsRaw, folderUID, "__folderUID")
	rawJSON, err := json.Marshal(jsRaw)
	if err != nil {
		return err
	}

	dirPath := filepath.Join(clonePath, "dashboards")
	os.MkdirAll(dirPath, os.ModePerm)

	if err := rewriteFile(filepath.Join(dirPath, slugExt), rawJSON); err != nil {
		return err
	}

	// If worktree is nil, it means that it hasn't been initialised, which means
	// the sync mode is "simple sync" and not Git.
	if worktree != nil {
		if _, err := worktree.Add(filepath.Join("dashboards", slugExt)); err != nil {
			return err
		}
	}

	return nil
}

func removeDashboardFromFilesystem(slug string, worktree *gogit.Worktree) (err error) {
	_, err = worktree.Remove(filepath.Join("dashboards", slug+".json"))
	return
}

// addLibraryChangesToRepo writes a library element content in a file, then adds the
// file to the git index, so it can be committed afterwards.
// Returns an error if there was an issue with either of the steps.
func addLibraryChangesToRepo(
	library *grafana.Library, clonePath string, worktree *gogit.Worktree, folderUID string) error {
	slugExt := library.Slug + ".json"
	// we take out the versions here, as versions are generated by grafana and
	// therefore can't be sanely sync'd across multiple grafana instances
	var jsRaw interface{}
	if err := json.Unmarshal([]byte(library.RawJSON), &jsRaw); err != nil {
		return err
	}
	// the following keys are unique only to an individual grafana instance
	dyno.Delete(jsRaw, "version")
	dyno.Delete(jsRaw, "id")
	// grafana 8.5 doesn't accept folderUID, needs folderID, folderIDs are only unique per grafana instance
	dyno.Set(jsRaw, folderUID, "__folderUID")
	rawJSON, err := json.Marshal(jsRaw)
	if err != nil {
		return err
	}

	dirPath := filepath.Join(clonePath, "libraries")
	os.MkdirAll(dirPath, os.ModePerm)

	if err := rewriteFile(filepath.Join(dirPath, slugExt), rawJSON); err != nil {
		return err
	}

	// If worktree is nil, it means that it hasn't been initialised, which means
	// the sync mode is "simple sync" and not Git.
	if worktree != nil {
		if _, err := worktree.Add(filepath.Join("libraries", slugExt)); err != nil {
			return err
		}
	}

	return nil
}

func removeLibraryFromFilesystem(slug string, worktree *gogit.Worktree) (err error) {
	_, err = worktree.Remove(filepath.Join("libraries", slug+".json"))
	return
}

// rewriteFile removes a given file and re-creates it with a new content. The
// content is provided as JSON, and is then indented before being written down.
// We need the whole "remove then recreate" thing because, if the file already
// exists, ioutil.WriteFile will append the content to it. However, we want to
// replace the oldest version with another (so git can diff it), so we re-create
// the file with the changed content.
// Returns an error if there was an issue when removing or writing the file, or
// indenting the JSON content.
func rewriteFile(filename string, content []byte) error {
	if err := os.Remove(filename); err != nil {
		pe, ok := err.(*os.PathError)
		if !ok || pe.Err.Error() != "no such file or directory" {
			return err
		}
	}

	indentedContent, err := indent(content)
	if err != nil {
		return err
	}

	return os.WriteFile(filename, indentedContent, 0644)
}

// indent indents a given JSON content with tabs.
// We need to indent the content as the Grafana API returns a one-lined JSON
// string, which isn't great to work with.
// Returns an error if there was an issue with the process.
func indent(srcJSON []byte) (indentedJSON []byte, err error) {
	buf := bytes.NewBuffer(nil)
	if err = json.Indent(buf, srcJSON, "", "\t"); err != nil {
		return
	}

	indentedJSON, err = io.ReadAll(buf)
	return
}
