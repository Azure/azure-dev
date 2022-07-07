import { QueryOptions } from "@testing-library/react";
import { Dispatch } from "react";
import { TodoItem } from "../models";
import { ItemService } from "../services/itemService";
import { ActionTypes } from "./common";
import config from "../config"
import { ActionMethod, createPayloadAction, PayloadAction } from "./actionCreators";

export interface ItemActions {
    list(listId: string, options?: QueryOptions): Promise<TodoItem[]>
    select(item?: TodoItem): Promise<TodoItem>
    load(listId: string, id: string): Promise<TodoItem>
    save(listId: string, Item: TodoItem): Promise<TodoItem>
    remove(listId: string, Item: TodoItem): Promise<void>
}

export const list = (listId: string, options?: QueryOptions): ActionMethod<TodoItem[]> => async (dispatch: Dispatch<ListItemsAction>) => {
    const itemService = new ItemService(config.api.baseUrl, `/lists/${listId}/items`);
    const items = await itemService.getList(options);

    dispatch(listItemsAction(items));

    return items;
}

export const select = (item?: TodoItem): ActionMethod<TodoItem | undefined> => async (dispatch: Dispatch<SelectItemAction>) => {
    dispatch(selectItemAction(item));

    return Promise.resolve(item);
}

export const load = (listId: string, id: string): ActionMethod<TodoItem> => async (dispatch: Dispatch<LoadItemAction>) => {
    const itemService = new ItemService(config.api.baseUrl, `/lists/${listId}/items`);
    const item = await itemService.get(id);

    dispatch(loadItemAction(item));

    return item;
}

export const save = (listId: string, item: TodoItem): ActionMethod<TodoItem> => async (dispatch: Dispatch<SaveItemAction>) => {
    const itemService = new ItemService(config.api.baseUrl, `/lists/${listId}/items`);
    const newItem = await itemService.save(item);

    dispatch(saveItemAction(newItem));

    return newItem;
}

export const remove = (listId: string, item: TodoItem): ActionMethod<void> => async (dispatch: Dispatch<DeleteItemAction>) => {
    const itemService = new ItemService(config.api.baseUrl, `/lists/${listId}/items`);
    if (item.id) {
        await itemService.delete(item.id);
        dispatch(deleteItemAction(item.id));
    }
}

export interface ListItemsAction extends PayloadAction<string, TodoItem[]> {
    type: ActionTypes.LOAD_TODO_ITEMS
}

export interface SelectItemAction extends PayloadAction<string, TodoItem | undefined> {
    type: ActionTypes.SELECT_TODO_ITEM
}

export interface LoadItemAction extends PayloadAction<string, TodoItem> {
    type: ActionTypes.LOAD_TODO_ITEM
}

export interface SaveItemAction extends PayloadAction<string, TodoItem> {
    type: ActionTypes.SAVE_TODO_ITEM
}

export interface DeleteItemAction extends PayloadAction<string, string> {
    type: ActionTypes.DELETE_TODO_ITEM
}

const listItemsAction = createPayloadAction<ListItemsAction>(ActionTypes.LOAD_TODO_ITEMS);
const selectItemAction = createPayloadAction<SelectItemAction>(ActionTypes.SELECT_TODO_ITEM);
const loadItemAction = createPayloadAction<LoadItemAction>(ActionTypes.LOAD_TODO_ITEM);
const saveItemAction = createPayloadAction<SaveItemAction>(ActionTypes.SAVE_TODO_ITEM);
const deleteItemAction = createPayloadAction<DeleteItemAction>(ActionTypes.DELETE_TODO_ITEM);
