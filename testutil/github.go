package testutil

import "github.com/google/go-github/github"

func NewGithubPREvent(prNumber int, baseRepoName, headRepoName, headHash, user, url string) *github.PullRequestEvent {
	return &github.PullRequestEvent{
		Action: github.String("opened"),
		Number: github.Int(prNumber),
		Repo: &github.Repository{
			FullName: github.String(baseRepoName),
		},
		Sender: &github.User{
			Login: github.String(user),
		},
		PullRequest: &github.PullRequest{
			DiffURL: github.String(url),
			Head: &github.PullRequestBranch{
				SHA: github.String(headHash),
				Repo: &github.Repository{
					FullName: github.String(headRepoName),
				},
			},
		},
	}
}
