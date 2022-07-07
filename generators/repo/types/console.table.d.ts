declare module 'console.table' {

    declare function getTable(dataRows: any[]): string
    declare function getTable(columnHeaders: string[], dataRows: any[]): string
    declare function getTable(columnHeader: string, dataRows: any[]): string
}