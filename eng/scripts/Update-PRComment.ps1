param(
    [string] $Repo,
    [string] $PRNumber,
    [string] $BodyFile,
    [string] $Tag
)

if ($Tag) {
    # Using --jq formats the JSON objects on separate lines which can be
    # parsed individually by PowerShell. Leaving --jq out results in the entire
    # paginated result set returning on a single line with no clear separating
    # sequence (i.e. "[{...}, {...}][{...},{...}]"). The result without --jq is
    # not parsable JSON because of the "][" sequence without a wrapping array.
    $commentsJsonRows = gh api `
        repos/$Repo/issues/$PrNumber/comments `
        --paginate `
        --jq '.[]'
    $comments = @()
    foreach ($row in $commentsJsonRows) {
        $comments +=@( ConvertFrom-Json $row )
    }

    Write-Host "Comments found: $($comments.Length)"

    $commentsToErase = $comments.Where({ $_.body.Contains($Tag) })
    foreach ($comment in $commentsToErase) {
        Write-Host "Deleting previous tagged comment $($comment.id)"
        gh api --method DELETE "repos/$Repo/issues/comments/$($comment.id)"
    }
}

Write-Host "Posting comment"
gh pr comment $PRNumber --repo "$Repo" --body-file $BodyFile
