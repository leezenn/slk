package cmd

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/auth"
	"github.com/spf13/cobra"
)

var downloadOutput string

var downloadCmd = &cobra.Command{
	Use:   "download <file-url>",
	Short: "Download a file attachment",
	Long: `Download a file from Slack using its url_private or url_private_download URL.

The auth token is sent automatically for private file URLs.`,
	Example: `  slk download https://files.slack.com/files-pri/T.../report.pdf
  slk download https://files.slack.com/... -o ~/Downloads/report.pdf`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fileURL := args[0]

		result, err := auth.GetToken()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		client := api.NewClient(result.Token)

		// Determine output filename
		outputPath := downloadOutput
		if outputPath == "" {
			outputPath = filenameFromURL(fileURL)
			if outputPath == "" {
				outputPath = "download"
			}
		}

		body, contentLength, err := client.DownloadFile(fileURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer body.Close()

		f, err := os.Create(outputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()

		// Show progress for files > 1MB
		if contentLength > 1<<20 {
			written, err := io.Copy(f, &progressReader{reader: body, total: contentLength})
			if err != nil {
				fmt.Fprintf(os.Stderr, "\nError writing file: %v\n", err)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "\n")
			fmt.Printf("Downloaded %s (%d bytes)\n", outputPath, written)
		} else {
			written, err := io.Copy(f, body)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error writing file: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Downloaded %s (%d bytes)\n", outputPath, written)
		}
	},
}

type progressReader struct {
	reader  io.Reader
	total   int64
	current int64
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.current += int64(n)
	if pr.total > 0 {
		pct := float64(pr.current) / float64(pr.total) * 100
		fmt.Fprintf(os.Stderr, "\rDownloading... %.0f%%", pct)
	}
	return n, err
}

func filenameFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	path := u.Path
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		name := parts[len(parts)-1]
		if name != "" {
			return filepath.Base(name)
		}
	}
	return ""
}

func init() {
	downloadCmd.Flags().StringVarP(&downloadOutput, "output", "o", "", "Output file path")
	rootCmd.AddCommand(downloadCmd)
}
