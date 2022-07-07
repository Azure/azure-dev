import { QueryOptions } from "@testing-library/react";
import { Dispatch } from "react";
import { TodoList } from "../models";
import { ListService } from "../services/listService";
import { ActionTypes } from "./common";
import config from "../config"
import { trackEvent } from "../services/telemetryService";
import { ActionMethod, createPayloadAction, PayloadAction } from "./actionCreators";

const listService = new ListService(config.api.baseUrl, '/lists');

export interface ListActions {
    list(options?: QueryOptions): Promise<TodoList[]>
    load(id: string): Promise<TodoList>
    select(list: TodoList): Promise<TodoList>
    save(list: TodoList): Promise<TodoList>
    remove(id: string): Promise<void>
}

export const list = (options?: QueryOptions): ActionMethod<TodoList[]> => async (dispatch: Dispatch<ListListsAction>) => {
    const lists = await listService.getList(options);

    dispatch(listListsAction(lists));

    return lists;
}

export const select = (list: TodoList): ActionMethod<TodoList> => (dispatch: Dispatch<SelectListAction>) => {
    dispatch(selectListAction(list));

    return Promise.resolve(list);
}

export const load = (id: string): ActionMethod<TodoList> => async (dispatch: Dispatch<LoadListAction>) => {
    const list = await listService.get(id);

    dispatch(loadListAction(list));

    return list;
}

export const save = (list: TodoList): ActionMethod<TodoList> => async (dispatch: Dispatch<SaveListAction>) => {
    const newList = await listService.save(list);

    dispatch(saveListAction(newList));

    trackEvent(ActionTypes.SAVE_TODO_LIST.toString());

    return newList;
}

export const remove = (id: string): ActionMethod<void> => async (dispatch: Dispatch<DeleteListAction>) => {
    await listService.delete(id);

    dispatch(deleteListAction(id));
}

export interface ListListsAction extends PayloadAction<string, TodoList[]> {
    type: ActionTypes.LOAD_TODO_LISTS
}

export interface SelectListAction extends PayloadAction<string, TodoList | undefined> {
    type: ActionTypes.SELECT_TODO_LIST
}

export interface LoadListAction extends PayloadAction<string, TodoList> {
    type: ActionTypes.LOAD_TODO_LIST
}

export interface SaveListAction extends PayloadAction<string, TodoList> {
    type: ActionTypes.SAVE_TODO_LIST
}

export interface DeleteListAction extends PayloadAction<string, string> {
    type: ActionTypes.DELETE_TODO_LIST
}

const listListsAction = createPayloadAction<ListListsAction>(ActionTypes.LOAD_TODO_LISTS);
const selectListAction = createPayloadAction<SelectListAction>(ActionTypes.SELECT_TODO_LIST);
const loadListAction = createPayloadAction<LoadListAction>(ActionTypes.LOAD_TODO_LIST);
const saveListAction = createPayloadAction<SaveListAction>(ActionTypes.SAVE_TODO_LIST);
const deleteListAction = createPayloadAction<DeleteListAction>(ActionTypes.DELETE_TODO_LIST);
