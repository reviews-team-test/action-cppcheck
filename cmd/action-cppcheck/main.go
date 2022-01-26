package main

import (
	"context"
	"encoding/xml"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"

	"github.com/bradleyfalzon/ghinstallation"
	"github.com/google/go-github/v42/github"
	"github.com/sourcegraph/go-diff/diff"
	"golang.org/x/sync/errgroup"
)

type CppCheckResults struct {
	XMLName xml.Name       `xml:"results"`
	Version string         `xml:"version,attr"`
	Errors  CppCheckErrors `xml:"errors"`
}
type CppCheckErrors struct {
	XMLName xml.Name        `xml:"errors"`
	Errors  []CppCheckError `xml:"error"`
}
type CppCheckError struct {
	XMLName  xml.Name          `xml:"error"`
	ID       string            `xml:"id,attr"`
	Severity string            `xml:"severity,attr"`
	Message  string            `xml:"msg,attr"`
	Verbose  string            `xml:"verbose,attr"`
	Location *CppCheckLocation `xml:"location"`
}
type CppCheckLocation struct {
	XMLName xml.Name `xml:"location"`
	File    string   `xml:"file,attr"`
	Line    int      `xml:"line,attr"`
}

func main() {
	var file, owner, repo string
	var pullID int
	var appID, installationID int64
	flag.StringVar(&repo, "repo", "peeweep-test/dde-dock", "owner and repo name")
	flag.StringVar(&file, "f", "/dev/stdin", "cppcheck result in xml format")
	flag.IntVar(&pullID, "pr", 8, "pull request id")
	flag.Int64Var(&appID, "app_id", 164400, "*github app id")
	flag.Int64Var(&installationID, "installation_id", 22221748, "*github installation id")
	flag.Parse()
	arr := strings.SplitN(repo, "/", 2)
	owner = arr[0]
	repo = arr[1]

	tr := http.DefaultTransport
	if privateKey := []byte(os.Getenv("PRIVATE_KEY")); len(privateKey) > 0 {
		var err error
		tr, err = ghinstallation.New(tr, appID, installationID, []byte(privateKey))
		if err != nil {
			log.Fatal(err)
		}
	} else if token := os.Getenv("GITHUB_TOKEN"); len(token) > 0 {
		tr = NewGitHubToken(tr, token)
	}
	client := github.NewClient(&http.Client{Transport: tr})

	var diffs []*diff.FileDiff
	var checkErrs []CppCheckError

	eg, ctx := errgroup.WithContext(context.Background())
	//get pull request diff
	eg.Go(func() error {
		diffRaw, _, err := client.PullRequests.GetRaw(ctx, owner, repo, pullID, github.RawOptions{Type: github.Diff})
		if err != nil {
			return fmt.Errorf("get diff: %w", err)
		}
		diffs, err = diff.ParseMultiFileDiff([]byte(diffRaw))
		if err != nil {
			return fmt.Errorf("parse diff: %w", err)
		}
		return nil
	})
	// get cppcheck result
	eg.Go(func() error {
		errors, err := decodeErrors(file)
		if err != nil {
			return err
		}
		checkErrs = errors
		return nil
	})
	err := eg.Wait()
	if err != nil {
		log.Fatal(err)
	}
	var comments []*github.DraftReviewComment
	for i := range diffs {
		filename := strings.TrimPrefix(diffs[i].NewName, "b/")
		for j := range diffs[i].Hunks {
			startline := int(diffs[i].Hunks[j].NewStartLine)
			endline := startline + int(diffs[i].Hunks[j].NewLines)
			for k := range checkErrs {
				if checkErrs[k].Location == nil {
					continue
				}
				if checkErrs[k].Location.File != filename {
					continue
				}
				if checkErrs[k].Location.Line < startline || checkErrs[k].Location.Line > endline {
					continue
				}
				line, body := checkErrs[k].Location.Line, checkErrs[k].Verbose
				comments = append(comments, &github.DraftReviewComment{
					Path: &filename,
					Line: &line,
					Body: &body,
				})
				log.Println(filename, checkErrs[k].Location.Line, checkErrs[k].Verbose)
			}
		}
	}
	if len(comments) > 0 {
		_, _, err := client.PullRequests.CreateReview(context.Background(), owner, repo, pullID,
			&github.PullRequestReviewRequest{
				Event:    github.String("REQUEST_CHANGES"),
				Body:     github.String("Good, but could be better"),
				Comments: comments,
			})
		if err != nil {
			log.Fatal(err)
		}
	} else {
		body := Words[rand.Intn(len(Words))]
		_, _, err := client.PullRequests.CreateReview(context.Background(), owner, repo, pullID,
			&github.PullRequestReviewRequest{
				Event: github.String("APPROVE"),
				Body:  &body,
			})
		if err != nil {
			log.Fatal(err)
		}
	}
}

func decodeErrors(fname string) ([]CppCheckError, error) {
	f, err := os.Open(fname)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()
	var result CppCheckResults
	err = xml.NewDecoder(f).Decode(&result)
	if err != nil {
		return nil, fmt.Errorf("decode xml: %w", err)
	}
	return result.Errors.Errors, nil
}
