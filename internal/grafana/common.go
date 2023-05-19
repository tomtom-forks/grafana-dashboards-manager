package grafana

import (
	"encoding/json"
	"github.com/bruce34/grafana-dashboards-manager/internal/config"
	"github.com/bruce34/grafana-dashboards-manager/internal/grafana/helpers"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

// FilterIgnored takes a map mapping files' names to their contents and remove
// all the files that are supposed to be ignored by the dashboard manager.
// An ignored file is either named "versions.json" or describing a dashboard
// which slug starts with a given prefix.
// Returns an error if the slug couldn't be tested against the prefix.
func FilterIgnored(
	filesToPush *map[string][]byte, cfg *config.Config,
) (err error) {
	for filename, content := range *filesToPush {
		max := len(content)
		if max > 40 {
			max = 40
		}
		logrus.WithFields(logrus.Fields{
			"filename": filename,
			"content":  string(content[:max]),
		}).Debug("Checking whether to ignore")
		// Don't set versions.json to be pushed
		if strings.HasSuffix(filename, "versions-metadata.json") {
			delete(*filesToPush, filename)
			continue
		}

		// Check if dashboard is ignored
		ignored, err := isIgnored(content, cfg)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"filename": filename,
				"err":      err,
				"content":  string(content),
			}).Info("Ignoring because of error")
			ignored = true
		}

		if ignored {
			delete(*filesToPush, filename)
		}
	}
	return
}

// PushDashboardFiles takes a slice of files' names and a map mapping a file's name to its
// content, and iterates over the first slice. For each file name, it will push
// to Grafana the content from the map that matches the name, as a creation or
// an update of an existing dashboard.
// Logs any errors encountered during an iteration, but doesn't return until all
// creation and/or update requests have been performed.
func PushDashboardFiles(filenames []string, contents map[string][]byte, versionsFile DefsFile, grafanaVersionFile DefsFile, client *Client) {
	// Push all files to the Grafana API
	for _, filename := range filenames {
		_, err := helpers.GetSlug(contents[filename])
		folderUID := ""
		if _, ok := contents[filename]; !ok {
			continue
		}
		if err == nil {
			var fld struct {
				FolderUID string `json:"__folderUID"`
			}
			err = json.Unmarshal(contents[filename], &fld)
			folderUID = fld.FolderUID
			logrus.WithFields(logrus.Fields{
				"folderUID": folderUID,
				"filename":  filename,
			}).Debug("Grafana: Create/Upload folderUID")
		} else {
			logrus.WithFields(logrus.Fields{
				"error":    err,
				"filename": filename,
			}).Error("Failed to find title")
		}
		logrus.WithFields(logrus.Fields{
			"folderUID": folderUID,
			"filename":  filename,
		}).Debug("Grafana: Create/Upload folderID")
		if err := client.CreateOrUpdateDashboard(contents[filename], folderUID); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":    err,
				"filename": filename,
			}).Error("Failed to push the file to Grafana")
		}
	}
}

func PushLibraryFiles(filenames []string, contents map[string][]byte, versionsFile DefsFile, grafanaVersionFile DefsFile, client *Client) {
	// Push all files to the Grafana API
	for _, filename := range filenames {
		_, err := helpers.GetSlug(contents[filename])
		if _, ok := contents[filename]; !ok {
			continue
		}

		var fld struct {
			FolderUID string `json:"__folderUID"`
			UID       string `json:"uid"`
		}
		err = json.Unmarshal(contents[filename], &fld)
		folderUID := fld.FolderUID
		uid := fld.UID

		if err == nil {
			logrus.WithFields(logrus.Fields{
				"folderUID": folderUID,
				"filename":  filename,
			}).Info("Grafana: Create/Upload library UID")
		} else {
			logrus.WithFields(logrus.Fields{
				"error":    err,
				"filename": filename,
			}).Error("Failed to find title")
		}
		libVersion, _ := versionsFile.LibraryVersionByUID[uid]

		if err := client.CreateOrUpdateLibrary(contents[filename], folderUID, libVersion); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":    err,
				"filename": filename,
			}).Error("Failed to push the file to Grafana")
		}
	}
}

// DeleteDashboards takes a slice of files' names and a map mapping a file's name
// to its content, and iterates over the first slice. For each file name, extract
// a dashboard's slug from the content, in the map, that matches the name, and
// will use it to send a deletion request to the Grafana API.
// Logs any errors encountered during an iteration, but doesn't return until all
// deletion requests have been performed.
func DeleteDashboards(filenames []string, contents map[string][]byte, client *Client) {
	for _, filename := range filenames {
		// Retrieve dashboard slug because we need it in the deletion request.
		slug, err := helpers.GetSlug(contents[filename])
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"error":    err,
				"filename": filename,
			}).Error("Failed to compute the dashboard's slug")
		}

		if err := client.DeleteDashboard(slug); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":    err,
				"filename": filename,
				"slug":     slug,
			}).Error("Failed to remove the dashboard from Grafana")
		}
	}
}

func DeleteLibraries(filenames []string, contents map[string][]byte, client *Client) {
	for _, filename := range filenames {
		var fld struct {
			UID string `json:"uid"`
		}
		err := json.Unmarshal(contents[filename], &fld)
		uid := fld.UID
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"error":    err,
				"filename": filename,
			}).Error("Failed to find the library UID")
		}

		if err := client.DeleteLibrary(uid); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":    err,
				"filename": filename,
				"uid":      uid,
			}).Error("Failed to remove the dashboard from Grafana")
		}
	}
}

// isIgnored checks whether the file must be ignored, by checking if there's an
// prefix for ignored files set in the configuration file, and if the dashboard
// described in the file has a name that starts with this prefix. Returns an
// error if there was an issue reading or decoding the file.
func isIgnored(dashboardJSON []byte, cfg *config.Config) (bool, error) {
	// If there's no prefix set, no file is ignored
	if len(cfg.Grafana.IgnorePrefix) == 0 {
		return false, nil
	}

	// Parse the file's content to extract its slug
	slug, err := helpers.GetSlug(dashboardJSON)
	if err != nil {
		return false, err
	}

	// Compare the slug against the prefix
	if strings.HasPrefix(slug, cfg.Grafana.IgnorePrefix) {
		return true, nil
	}

	return false, nil
}

func Push(cfg *config.Config, fileVersionFile DefsFile, grafanaVersionFile DefsFile,
	dashboardFiles []string, dashboardContents map[string][]byte, client *Client) (err error) {
	// Filter out all dashboardFiles that are supposed to be ignored by the
	// dashboard manager.
	if err = FilterIgnored(&dashboardContents, cfg); err != nil {
		return err
	}

	// Push the dashboardContents of the dashboardFiles that were added or modified to the
	// Grafana API.
	PushDashboardFiles(dashboardFiles, dashboardContents, fileVersionFile, grafanaVersionFile, client)
	return
}

// getFilesContents takes a slice of files' names and a map mapping a file's name
// to its content and appends to it the current content of all of the files for
// which the name appears in the slice.
// Returns an error if there was an issue reading a file.
func GetFilesContents(
	filenames []string, contents *map[string][]byte, subdir string, cfg *config.Config,
) (err error) {
	// Iterate over files' names
	for _, filename := range filenames {
		// Compute the file's path
		filePath := filepath.Join(cfg.Git.ClonePath, subdir, filename)
		// Read the file's content
		fileContent, err := ioutil.ReadFile(filePath)
		if err != nil {
			return err
		}

		// Append the content to the map
		(*contents)[filename] = fileContent
	}
	return
}

func LoadFilesFromDirectory(cfg *config.Config, dir string, subdir string) (filenames []string, contents map[string][]byte, err error) {
	filenames = make([]string, 0)
	contents = make(map[string][]byte)
	files, err := os.ReadDir(filepath.Join(dir, subdir))
	if err != nil {
		return
	}
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".json") {
			filenames = append(filenames, file.Name())
		}
	}
	err = GetFilesContents(filenames, &contents, subdir, cfg)
	return
}
