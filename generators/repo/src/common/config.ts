export const isDebug = (): boolean => {
    return !!process.env.REPOMAN_DEBUG == true
}

export const setDebug = (debug: boolean): void => {
    process.env.REPOMAN_DEBUG = debug ? 'true' : 'false';
}