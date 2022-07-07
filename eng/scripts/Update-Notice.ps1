param(
    [string] $NoticeSourcePath
)

$existingNoticeFiles = Get-ChildItem -Path "$PSScriptRoot/../../" -Recurse NOTICE.txt
foreach ($noticeFile in $existingNoticeFiles) {
    Write-Host "Copying over $($noticeFile)"
    Copy-Item $NoticeSourcePath $noticeFile -Force
}
