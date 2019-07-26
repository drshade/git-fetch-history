package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	git "gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/src-d/go-git.v4/plumbing/format/diff"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
	"gopkg.in/src-d/go-git.v4/plumbing/transport"
	gitssh "gopkg.in/src-d/go-git.v4/plumbing/transport/ssh"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

type CommitEntry struct {
	Repo      string      `json:"repo"`
	Branch    string      `json:"branch"`
	Hash      string      `json:"hash"`
	Timestamp int64       `json:"timestamp"`
	Author    string      `json:"author"`
	Message   string      `json:"message"`
	Files     []FileEntry `json:"files"`
}

type FileEntry struct {
	File          string `json:"file"`
	ChangeType    string `json:"changetype"`
	ChunksAdded   int    `json:"chunksadded"`
	ChunksRemoved int    `json:"chunksremoved"`
	LinesAdded    int    `json:"linesadded"`
	LinesRemoved  int    `json:"linesremoved"`
}

const (
	AWS_REGION = "eu-west-1"
	AWS_BUCKET = "git-fetch-history"
)

// CheckIfError should be used to naively panics if an error is not nil.
func CheckIfError(err error) {
	if err == nil {
		return
	}

	fmt.Println("Handling error:", fmt.Sprintf("error: %s", err))
	fmt.Println("Sleeping 5 minutes pre-death!")
	time.Sleep(5 * time.Minute)
	fmt.Println("And now dead...")
	os.Exit(1)
}

func UploadToS3(entry *CommitEntry) {
	sess, err := session.NewSession(&aws.Config{Region: aws.String(AWS_REGION)})
	CheckIfError(err)

	key := fmt.Sprintf("%s/%s/%s.json", entry.Repo, entry.Branch, entry.Hash)

	jsonBody, err := json.Marshal(entry)
	CheckIfError(err)

	fmt.Println("Uploading", key)
	_, err = s3.New(sess).PutObject(&s3.PutObjectInput{
		Bucket:               aws.String(AWS_BUCKET),
		Key:                  aws.String(key),
		ACL:                  aws.String("private"),
		Body:                 bytes.NewReader(jsonBody),
		ContentLength:        aws.Int64(int64(len(jsonBody))),
		ContentType:          aws.String("application/json"),
		ContentDisposition:   aws.String("attachment"),
		ServerSideEncryption: aws.String("AES256"),
	})
	CheckIfError(err)
}

func main() {
	fmt.Println("Sleep for a bit to let envoy settle")
	time.Sleep(30 * time.Second)

	repoName := os.Getenv("REPO")
	branchName := os.Getenv("BRANCH")
	fmt.Println("REPO", repoName, "BRANCH", branchName)

	if repoName == "" {
		panic("$REPO not set")
	}
	if branchName == "" {
		panic("$BRANCH not set")
	}

	publicKeys, err := gitssh.NewPublicKeysFromFile("tomwells", "./temp", "")
	CheckIfError(err)

	// Disable known hosts checking
	//
	publicKeys.HostKeyCallback = ssh.InsecureIgnoreHostKey()

	repo := Setup(repoName, branchName, publicKeys)

	// Track head
	//
	worktree, err := repo.Worktree()
	head, err := repo.Head()
	CheckIfError(err)
	fmt.Println("New head is", head.Hash())

	for true {
		time.Sleep(5 * time.Minute) // avoid aws calling us ssh abusers
		//fmt.Println("Checking for changes")
		err = worktree.Pull(&git.PullOptions{
			Auth: publicKeys,
		})
		if err == git.NoErrAlreadyUpToDate {
			//fmt.Println("Already upto date...")
		} else {
			CheckIfError(err)
			newhead, err := repo.Head()
			CheckIfError(err)
			fmt.Println("New head is", newhead.Hash(), "old head is", head.Hash())

			// Always from HEAD downwards
			//
			commitIter, err := repo.Log(&git.LogOptions{})
			CheckIfError(err)

			hitOldHead := false
			commitIter.ForEach(func(commit *object.Commit) error {
				if commit.Hash.String() == head.Hash().String() {
					hitOldHead = true
				}
				if hitOldHead {
					//fmt.Println("Ignoring commit", commit.Hash.String())
					return nil
				}

				// Process the commit
				//
				ProcessCommit(repoName, branchName, commit)

				return nil
			})

			head = newhead
		}
	}
}

func setupAndGetPath(repoName string) string {
	// Fix directories that may already exist etc
	//
	repoPath := fmt.Sprintf("/tmp/repo/%s/", repoName)
	if _, err := os.Stat(repoPath); err == nil {
		fmt.Println("Deleting old", repoPath)
		os.RemoveAll(repoPath)
	}

	fmt.Println("Creating", repoPath, "...")
	os.MkdirAll(repoPath, 0700)

	return repoPath
}

func ProcessCommit(repoName string, branchName string, commit *object.Commit) {
	// Process the commit message
	//
	message := strings.Replace(commit.Message, "\n", "", -1)

	fmt.Println(" Commit hash:", commit.Hash)
	fmt.Println("      author:", commit.Author.Email)
	fmt.Println("        date:", commit.Author.When)
	fmt.Println("     message:", message)

	entry := &CommitEntry{
		Repo:      repoName,
		Branch:    branchName,
		Hash:      commit.Hash.String(),
		Timestamp: commit.Author.When.Unix(),
		Author:    commit.Author.Email,
		Message:   message,
	}
	fileEntries := make([]FileEntry, 0)

	/*
		stats, err := commit.Stats()
		CheckIfError(err)
		fmt.Println("Stats:", stats)
	*/

	if commit.NumParents() == 0 {
		// A first time commit, ie a list of files, each with single chunks
		//
		fileIter, err := commit.Files()
		CheckIfError(err)

		fileIter.ForEach(func(file *object.File) error {
			lines, err := file.Lines()
			CheckIfError(err)

			fmt.Println("        File:", file.Name, "added:", len(lines), "removed:", 0, "chunks added: 1, chunks removed: 0 (first commit)")

			fileEntry := FileEntry{
				File:          file.Name,
				ChangeType:    "initial",
				ChunksAdded:   1,
				ChunksRemoved: 0,
				LinesAdded:    len(lines),
				LinesRemoved:  0,
			}
			fileEntries = append(fileEntries, fileEntry)

			return nil
		})
	} else {
		// A patch - a list of files, each with multiple chunks
		//
		fmt.Println("NumParents:", commit.NumParents())
		parent, err := commit.Parent(0)
		CheckIfError(err)

		fmt.Println("Parent(0)", parent.Hash.String())

		isAncestor, err := parent.IsAncestor(commit)
		CheckIfError(err)

		if !isAncestor {
			fmt.Println("Ignoring this one - because the parent is not an ancestor!")
		} else {
			patch, err := parent.Patch(commit)
			CheckIfError(err)

			filepatches := patch.FilePatches()
			for fileidx := 0; fileidx < len(filepatches); fileidx++ {
				filepatch := filepatches[fileidx]

				fromFile, toFile := filepatch.Files()
				//fmt.Println("fromFile", fromFile)
				//fmt.Println("toFile", toFile)

				if toFile == nil && fromFile == nil {
					fmt.Println(" FromFile and ToFile are both nil... WTF?")
				} else if toFile == nil {
					// File deleted
					//
					fmt.Println(" Delete File:", fromFile.Path())
					fileEntry := FileEntry{
						File:          fromFile.Path(),
						ChangeType:    "deleted",
						ChunksAdded:   0,
						ChunksRemoved: 0,
						LinesAdded:    0,
						LinesRemoved:  0,
					}
					fileEntries = append(fileEntries, fileEntry)
				} else {

					linesadded, linesremoved := 0, 0
					chunksadded, chunksremoved := 0, 0
					chunks := filepatch.Chunks()
					for chunkidx := 0; chunkidx < len(chunks); chunkidx++ {
						chunk := chunks[chunkidx]

						if chunk.Type() == diff.Equal {
						}
						if chunk.Type() == diff.Add {
							linesadded += len(strings.Split(chunk.Content(), "\n"))
							chunksadded++
						}
						if chunk.Type() == diff.Delete {
							linesremoved += len(strings.Split(chunk.Content(), "\n"))
							chunksremoved++
						}
					}
					fmt.Println("        File:", toFile.Path(), "lines added:", linesadded, "lines removed:", linesremoved, "chunks added:", chunksadded, "chunks removed:", chunksremoved)
					fileEntry := FileEntry{
						File:          toFile.Path(),
						ChangeType:    "modify",
						ChunksAdded:   chunksadded,
						ChunksRemoved: chunksremoved,
						LinesAdded:    linesadded,
						LinesRemoved:  linesremoved,
					}
					fileEntries = append(fileEntries, fileEntry)
				}
			}
		}

	}

	entry.Files = fileEntries
	UploadToS3(entry)
}

func Setup(repoName string, branchName string, authMethod transport.AuthMethod) *git.Repository {

	repoPath := setupAndGetPath(repoName)

	// Clones the given repository in memory, creating the remote, the local
	// branches and fetching the objects, exactly as:
	repoURL := fmt.Sprintf("git@bitbucket.org:synthesis_admin/%s.git", repoName)
	fmt.Println("Cloning", repoURL, "...")

	// -- METHOD 1 (which doesn't work all the time)
	// -- resulting in error: close tcp xx.xx.xx.xx:xxxx->xx.xx.xx.xx:22: use of closed network connection
	// -- (which seems to be a bug in GO running on linux)
	var err error
	var repo *git.Repository
	workaround := true
	if workaround {
		// Execute the git command via the shell
		//
		fmt.Println("Using workaround!")

		os.Setenv("GIT_SSH_COMMAND", "ssh -i ./temp")
		cmd := exec.Command("git", "clone", repoURL, repoPath)
		stdout, err := cmd.CombinedOutput()
		fmt.Println(string(stdout))
		CheckIfError(err)

		// Now just open it
		//
		repo, err = git.PlainOpen(repoPath)
	} else {

		repo, err = git.PlainClone(repoPath, false, &git.CloneOptions{
			URL:          repoURL,
			Auth:         authMethod,
			SingleBranch: false,
		})
		CheckIfError(err)
	}

	fmt.Println("Fetching commits...")
	refs, err := repo.Storer.IterReferences()
	refs.ForEach(func(branch *plumbing.Reference) error {

		// Only deal with single branch (for now)
		//
		if branch.Name().String() == branchName {

			fmt.Println("Setting up for:", branch.Name().String())

			logIter, err := repo.Log(&git.LogOptions{From: branch.Hash()})
			CheckIfError(err)

			fmt.Println("")
			logIter.ForEach(func(commit *object.Commit) error {
				ProcessCommit(repoName, branchName, commit)
				fmt.Println("")
				return nil
			})
		} else {
			fmt.Println("Ignoring Branch:", branch.Name().String())
		}
		return nil
	})
	return repo
}
