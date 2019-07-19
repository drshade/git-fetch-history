package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/lambda"
	"golang.org/x/crypto/ssh"

	git "gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/src-d/go-git.v4/plumbing/format/diff"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
	gitssh "gopkg.in/src-d/go-git.v4/plumbing/transport/ssh"
	"gopkg.in/src-d/go-git.v4/storage/memory"
)

// Info should be used to describe the example commands that are about to run.
func Info(format string, args ...interface{}) {
	fmt.Printf("\x1b[34;1m%s\x1b[0m\n", fmt.Sprintf(format, args...))
}

// Warning should be used to display a warning
func Warning(format string, args ...interface{}) {
	fmt.Printf("\x1b[36;1m%s\x1b[0m\n", fmt.Sprintf(format, args...))
}

// CheckIfError should be used to naively panics if an error is not nil.
func CheckIfError(err error) {
	if err == nil {
		return
	}

	fmt.Printf("\x1b[31;1m%s\x1b[0m\n", fmt.Sprintf("error: %s", err))
	os.Exit(1)
}

type LambdaArguments struct {
	Repo string `json:"repo"`
}

func HandleLambdaRequest(ctx context.Context, args LambdaArguments) (string, error) {
	Commits(args.Repo)
	return fmt.Sprintf("Hello %s!", args.Repo), nil
}

func main() {
	os.Setenv("SSH_KNOWN_HOSTS", "./known_hosts")
	if os.Getenv("LAMBDA") != "" {
		fmt.Println("Running as a lambda function")
		lambda.Start(HandleLambdaRequest)
	} else {
		fmt.Println("Running directly")
		Commits("zkp")
	}
}

func Commits(repoName string) {

	publicKeys, err := gitssh.NewPublicKeysFromFile("tomwells", "./temp", "")
	CheckIfError(err)

	// Disable known hosts checking
	//
	publicKeys.HostKeyCallback = ssh.InsecureIgnoreHostKey()

	// Clones the given repository in memory, creating the remote, the local
	// branches and fetching the objects, exactly as:
	repoURL := fmt.Sprintf("git@bitbucket.org:synthesis_admin/%s.git", repoName)
	fmt.Println("Cloning", repoURL, "...")
	repo, err := git.Clone(memory.NewStorage(), nil, &git.CloneOptions{
		URL:          repoURL,
		Auth:         publicKeys,
		SingleBranch: false,
	})
	CheckIfError(err)

	// ... retrieves the branch pointed by HEAD

	fmt.Println("Fetching commits...")
	refs, err := repo.Storer.IterReferences()
	refs.ForEach(func(branch *plumbing.Reference) error {

		// Only deal with master branch (for now)
		//
		if branch.Name() == "refs/remotes/origin/master" {

			fmt.Println("Remote Branch:", branch.Name())

			logIter, err := repo.Log(&git.LogOptions{From: branch.Hash()})
			CheckIfError(err)

			logIter.ForEach(func(commit *object.Commit) error {
				fmt.Println(" Commit hash:", commit.Hash)
				fmt.Println("      author:", commit.Author.Email)
				fmt.Println("        date:", commit.Author.When)
				fmt.Println("     message:", commit.Message)

				if commit.NumParents() > 0 {
					parent, err := commit.Parent(0)
					CheckIfError(err)

					patch, err := parent.Patch(commit)
					CheckIfError(err)

					//fmt.Println("Stats: ", patch.Stats())

					linesadded, linesremoved := 0, 0
					filepatches := patch.FilePatches()
					for fileidx := 0; fileidx < len(filepatches); fileidx++ {
						filepatch := filepatches[fileidx]
						chunks := filepatch.Chunks()
						for chunkidx := 0; chunkidx < len(chunks); chunkidx++ {
							chunk := chunks[chunkidx]

							if chunk.Type() == diff.Equal {
							}
							if chunk.Type() == diff.Add {
								linesadded += len(strings.Split(chunk.Content(), "\n"))
							}
							if chunk.Type() == diff.Delete {
								linesremoved += len(strings.Split(chunk.Content(), "\n"))
							}
						}
					}

					fmt.Println("Lines added:", linesadded, "removed:", linesremoved)
				}
				return nil
			})
		}
		return nil
	})
}
