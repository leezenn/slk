package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

var slackFileIDRe = regexp.MustCompile(`^F[A-Z0-9]{8,}$`)

type downloadOptions struct {
	output string
	force  bool
}

type downloadSource struct {
	URL      string
	Filename string
}

type downloadResult struct {
	FileID string `json:"file_id"`
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
}

type downloadJSONOutput struct {
	OK       bool           `json:"ok"`
	Download downloadResult `json:"download"`
}

type fileInfoGetter interface {
	GetFileInfo(fileID string) (*api.File, error)
}

func newDownloadCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	options := &downloadOptions{}
	command := &cobra.Command{
		Use:   "download <file-id>",
		Short: "Download a file attachment",
		Long: `Download a Slack attachment using the stable file ID shown in message output.

The file ID is resolved through Slack's files.info API. Private download URLs
remain internal to the transfer. Existing output files are never overwritten
unless --force is set.`,
		Example: `  slk download F0123456789
  slk download F0123456789 -o report.pdf
  slk download F0123456789 -o report.pdf --force
  slk download F0123456789 --json`,
		Args: validateDownloadArgs,
	}
	command.Flags().StringVarP(&options.output, "output", "o", "", "Output file path")
	command.Flags().BoolVar(&options.force, "force", false, "Overwrite the output file if it already exists")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := getClient(cmd, deps)
		if err != nil {
			return err
		}
		fileID := args[0]
		source, err := resolveDownloadSource(client, fileID)
		if err != nil {
			return slackAPIError(err)
		}

		outputPath := options.output
		if outputPath == "" {
			outputPath = source.Filename
			if outputPath == "" {
				outputPath = "download"
			}
		}
		if err := ensureDownloadDestinationAvailable(outputPath, options.force); err != nil {
			if errors.Is(err, os.ErrExist) {
				return refusedDownloadPath(outputPath)
			}
			return filesystemError(err)
		}
		if err := checkContext(cmd.Context()); err != nil {
			return err
		}

		body, contentLength, err := client.DownloadFile(source.URL)
		if err != nil {
			return slackAPIError(err)
		}
		defer body.Close()

		var reader io.Reader = body
		if contentLength > 1<<20 && rootOptions.verbose {
			reader = &progressReader{reader: body, total: contentLength, out: cmd.ErrOrStderr()}
		}
		written, err := writeDownloadFile(outputPath, options.force, reader)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return refusedDownloadPath(outputPath)
			}
			return filesystemError(err)
		}
		if contentLength > 1<<20 && rootOptions.verbose {
			fmt.Fprintln(cmd.ErrOrStderr())
		}
		output, err := formatDownloadResult(downloadResult{FileID: fileID, Path: outputPath, Bytes: written}, rootOptions.json)
		if err != nil {
			return internalError()
		}
		fmt.Fprintln(cmd.OutOrStdout(), output)
		return nil
	}
	return command
}

func refusedDownloadPath(path string) error {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = filepath.Clean(absolute)
	}
	return refusedError("output already exists: "+safeDynamic(path, 4096), "Choose another path or pass --force.")
}

type progressReader struct {
	reader  io.Reader
	out     io.Writer
	total   int64
	current int64
	lastPct int
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.current += int64(n)
	if pr.total > 0 {
		pct := int(float64(pr.current) / float64(pr.total) * 100)
		if pct != pr.lastPct {
			pr.lastPct = pct
			fmt.Fprintf(pr.out, "\rDownloading... %d%%", pct)
		}
	}
	return n, err
}

func formatDownloadResult(result downloadResult, asJSON bool) (string, error) {
	if asJSON {
		return format.FormatJSON(downloadJSONOutput{OK: true, Download: result})
	}
	return fmt.Sprintf("Downloaded %s (%d bytes) from %s", result.Path, result.Bytes, result.FileID), nil
}

func validateDownloadArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(1)(cmd, args); err != nil {
		return invalidArgument(cmd, err.Error())
	}
	if err := validateSlackFileID(args[0]); err != nil {
		return invalidArgument(cmd, err.Error())
	}
	return nil
}

func validateSlackFileID(fileID string) error {
	if !slackFileIDRe.MatchString(fileID) {
		return errors.New("invalid Slack file ID; expected an ID like F0123456789")
	}
	return nil
}

func resolveDownloadSource(client fileInfoGetter, fileID string) (downloadSource, error) {
	if err := validateSlackFileID(fileID); err != nil {
		return downloadSource{}, err
	}
	file, err := client.GetFileInfo(fileID)
	if err != nil {
		return downloadSource{}, fmt.Errorf("resolving Slack file %s: %w", fileID, err)
	}
	fileURL := file.URLPrivateDownload
	if fileURL == "" {
		fileURL = file.URLPrivate
	}
	if fileURL == "" {
		return downloadSource{}, fmt.Errorf("Slack file %s has no private download URL", fileID)
	}

	filename := filepath.Base(file.Name)
	if filename == "." || filename == string(filepath.Separator) {
		filename = ""
	}
	if filename == "" {
		filename = filenameFromURL(fileURL)
	}
	return downloadSource{URL: fileURL, Filename: filename}, nil
}

func ensureDownloadDestinationAvailable(path string, force bool) error {
	if force {
		return nil
	}
	_, err := os.Lstat(path)
	if err == nil {
		return fmt.Errorf("%s: %w", path, os.ErrExist)
	}
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func writeDownloadFile(path string, force bool, reader io.Reader) (int64, error) {
	var (
		preservedMode    os.FileMode
		hasPreservedMode bool
	)
	if force {
		info, err := os.Stat(path)
		if err == nil {
			preservedMode = info.Mode().Perm()
			hasPreservedMode = true
		} else if !os.IsNotExist(err) {
			return 0, err
		}
	}

	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".slk-*")
	if err != nil {
		return 0, err
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()

	if hasPreservedMode {
		if err := temp.Chmod(preservedMode); err != nil {
			return 0, err
		}
	}
	written, err := io.Copy(temp, reader)
	if err != nil {
		return written, err
	}
	if err := temp.Sync(); err != nil {
		return written, err
	}
	if err := temp.Close(); err != nil {
		return written, err
	}

	if force {
		if err := os.Rename(tempPath, path); err != nil {
			return written, err
		}
		return written, nil
	}
	if err := os.Link(tempPath, path); err != nil {
		return written, err
	}
	return written, nil
}

func filenameFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) > 0 && parts[len(parts)-1] != "" {
		return filepath.Base(parts[len(parts)-1])
	}
	return ""
}
