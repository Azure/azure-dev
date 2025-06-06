import { Reducer } from "react";
import { ActionTypes, TodoActions } from "../actions/common";
import { TodoList } from "../models"

export const listsReducer: Reducer<TodoList[], TodoActions> = (state: TodoList[], action: TodoActions): TodoList[] => {
    switch (action.type) {
        case ActionTypes.LOAD_TODO_LISTS:
            state = [...action.payload];
            break;
        case ActionTypes.SAVE_TODO_LIST:
            state = [...state, action.payload];
            break;
        case ActionTypes.DELETE_TODO_LIST:
            state = [...state.filter(list => list.id !== action.payload)]
    }

    return state;
}