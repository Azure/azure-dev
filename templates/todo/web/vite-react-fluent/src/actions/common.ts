import * as itemActions from './itemActions';
import * as listActions from './listActions';

export enum ActionTypes {
    LOAD_TODO_LISTS = "LOAD_TODO_LISTS",
    LOAD_TODO_LIST = "LOAD_TODO_LIST",
    SELECT_TODO_LIST = "SELECT_TODO_LIST",
    SAVE_TODO_LIST = "SAVE_TODO_LIST",
    DELETE_TODO_LIST = "DELETE_TODO_LIST",
    LOAD_TODO_ITEMS = "LOAD_TODO_ITEMS",
    LOAD_TODO_ITEM = "LOAD_TODO_ITEM",
    SELECT_TODO_ITEM = "SELECT_TODO_ITEM",
    SAVE_TODO_ITEM = "SAVE_TODO_ITEM",
    DELETE_TODO_ITEM = "DELETE_TODO_ITEM"
}

export type TodoActions =
    itemActions.ListItemsAction |
    itemActions.SelectItemAction |
    itemActions.LoadItemAction |
    itemActions.SaveItemAction |
    itemActions.DeleteItemAction |
    listActions.ListListsAction |
    listActions.SelectListAction |
    listActions.LoadListAction |
    listActions.SaveListAction |
    listActions.DeleteListAction;