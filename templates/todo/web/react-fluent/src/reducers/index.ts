import { Reducer } from "react";
import { TodoActions } from "../actions/common";
import { listsReducer } from "./listsReducer";
import { selectedItemReducer } from "./selectedItemReducer";
import { selectedListReducer } from "./selectedListReducer";

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const combineReducers = (slices: {[key: string]: Reducer<any, TodoActions>}) => (prevState: any, action: TodoActions) =>
    Object.keys(slices).reduce(
        (nextState, nextProp) => ({
            ...nextState,
            [nextProp]: slices[nextProp](prevState[nextProp], action)
        }),
        prevState
    );

export default combineReducers({
    lists: listsReducer,
    selectedList: selectedListReducer,
    selectedItem: selectedItemReducer,
});
