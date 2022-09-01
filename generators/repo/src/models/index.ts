export interface RepoManifest {
    metadata: {
        type: string
        name: string
        description: string
    }
    repo: {
        includeProjectAssets: boolean
        remotes: GitRemote[]

        assets: AssetRule[]
        rewrite?: {
            rules: RewriteRule[]
        }
    }
}

export interface GitRemote {
    name: string
    url: string
    branch?: string
}

export interface AssetRule {
    from: string
    to: string
    patterns?: string[]
    ignore?: string[]
}

export type RewriteRule = AssetRule;

export interface RepomanCommandOptions {
    debug: true
    [key: string]: any
}

export interface RepomanCommand {
    execute(): Promise<void>
}