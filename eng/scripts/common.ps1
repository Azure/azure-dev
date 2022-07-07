function Secretize($str, $revealChars = 2) {
    if ($revealChars -ge $str.Length) {
        # In cases where $revealChars is greater than or equal to the length of
        # the string, secretize the entire string so as not to reveal the value
        return "*" * $str.Length
    }
    return "$($str.Substring(0,$revealChars))$("*" * ($str.Length - $revealChars))"
}

function GetInvitations($repo) {
    $path = "repos/$repo/invitations"
    $invitationsResponseJson = gh api $path --paginate
    if ($LASTEXITCODE) {
        throw "Could not fetch invitations"
    }

    return ConvertFrom-Json $invitationsResponseJson
}
