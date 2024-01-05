import { TodoItem } from "./todoItem";

export interface TodoList {
    id?: string
    name: string
    items?: TodoItem[]
    description?: string
    createdDate?: Date
    updatedDate?: Date
}