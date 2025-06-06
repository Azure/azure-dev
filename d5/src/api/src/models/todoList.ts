import mongoose, { Schema } from "mongoose";

export type TodoList = {
    id: string
    name: string
    description?: string
    createdDate?: Date
    updatedDate?: Date
}

const schema = new Schema({
    name: {
        type: String,
        required: true,
    },
    description: String
}, {
    timestamps: {
        createdAt: "createdDate",
        updatedAt: "updatedDate"
    }
});

export const TodoListModel = mongoose.model<TodoList>("TodoList", schema, "TodoList");