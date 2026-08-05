const numericIdentifier = '(?:0|[1-9]\\d*)'
const prereleaseIdentifier = `(?:${numericIdentifier}|\\d*[A-Za-z-][0-9A-Za-z-]*)`
const semanticVersion =
    `${numericIdentifier}\\.${numericIdentifier}\\.${numericIdentifier}` +
    `(?:-${prereleaseIdentifier}(?:\\.${prereleaseIdentifier})*)?` +
    '(?:\\+[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?'
// Keep aliases aligned with the versions supported by cli/installer/install-azd.*.
const validVersionPattern = new RegExp(`^(?:latest|stable|daily|${semanticVersion})$`)

export function isValidVersion(version: string): boolean {
    return version.length <= 128 && validVersionPattern.exec(version)?.[0] === version
}
