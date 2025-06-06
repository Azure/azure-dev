import { Reducer } from "react";
import { ActionTypes, TodoActions } from "../actions/common";
import { TodoList } from "../models"

export const selectedListReducer: Reducer<TodoList | undefined, TodoActions> = (state: TodoList | undefined, action: TodoActions) => {
    switch (action.type) {
        case ActionTypes.SELECT_TODO_LIST:
        case ActionTypes.LOAD_TODO_LIST:
            state = action.payload ? { ...action.payload } : undefined;
            break;
        case ActionTypes.DELETE_TODO_LIST:
            if (state && state.id === action.payload) {
                state = undefined;
            }
            break;
        case ActionTypes.LOAD_TODO_ITEMS:
            if (state) {
                state.items = [...action.payload];
            }
            break;
        case ActionTypes.SAVE_TODO_ITEM:
            if (state) {
                const items = [...state.items || []];
                const index = items.findIndex(item => item.id === action.payload.id);
                if (index > -1) {
                    items.splice(index, 1, action.payload);
                    state.items = items;
                } else {
                    state.items = [...items, action.payload];
                }
            }
            break;
        case ActionTypes.DELETE_TODO_ITEM:
            if (state) {
                state.items = [...(state.items || []).filter(item => item.id !== action.payload)];
            }
            break;
    }

    return state;
}