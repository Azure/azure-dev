import mongoose, { Schema } from "mongoose";

export enum TodoItemState {
    Todo = "todo",
    InProgress = "inprogress",
    Done = "done"
}

export type TodoItem = {
    id: mongoose.Types.ObjectId
    listId: mongoose.Types.ObjectId
    name: string
    state: TodoItemState
    description?: string
    dueDate?: Date
    completedDate?: Date
    createdDate?: Date
    updatedDate?: Date
}

const schema = new Schema({
    listId: {
        type: Schema.Types.ObjectId,
        required: true
    },
    name: {
        type: String,
        required: true
    },
    description: String,
    state: {
        type: String,
        required: true,
        default: TodoItemState.Todo
    },
    dueDate: Date,
    completedDate: Date,
}, {
    timestamps: {
        createdAt: "createdDate",
        updatedAt: "updatedDate"
    }
});

export const TodoItemModel = mongoose.model<TodoItem>("TodoItem", schema, "TodoItem");