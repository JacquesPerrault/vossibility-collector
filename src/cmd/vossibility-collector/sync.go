package main

import "github.com/codegangsta/cli"

var syncCommand = cli.Command{
	Name:   "sync",
	Usage:  "sync storage with the Github repositories",
	Action: doSyncCommand,
	Flags: []cli.Flag{
		cli.IntFlag{Name: "from", Value: 1, Usage: "issue number to start from"},
		cli.IntFlag{Name: "sleep", Value: 0, Usage: "sleep delay between each GitHub page queried"},
	},
}

// doSyncCommand runs a synchronization job: it fetches all GitHub issues and
// pull requests starting with the From index. It uses the API pagination to
// reduce API calls, and allows a Sleep delay between each page to avoid
// triggering the abuse detection mechanism.
func doSyncCommand(c *cli.Context) {
	config := ParseConfigOrDie(c.GlobalString("config"))
	client := NewClient(config)
	blobStore := NewTransformingBlobStore(config.Transformations)

	// Get the list of repositories.
	repos := make([]*Repository, 0, len(config.Repositories))
	for _, r := range config.Repositories {
		repos = append(repos, r)
	}

	// Configure a syncJob taking all issues (opened and closed) and storing
	// in the snapshot store.
	syncOptions := DefaultSyncOptions
	syncOptions.From = c.Int("from")
	syncOptions.SleepPerPage = c.Int("sleep")
	syncOptions.State = GithubStateFilterAll
	syncOptions.Storage = StoreSnapshot

	// Create and run the synchronization job.
	NewSyncCommandWithOptions(client, blobStore, &syncOptions).Run(repos)
}