/* eslint-disable @typescript-eslint/no-explicit-any */
import { Dispatch } from "react";

export interface Action<T = any> {
    type: T
}

export interface AnyAction extends Action {
    [extraProps: string]: any
}

export interface ActionCreator<A, P extends any[] = any[]> {
    (...args: P): A
}

export interface ActionCreatorsMapObject<A = any, P extends any[] = any[]> {
    [key: string]: ActionCreator<A, P>
}

export type ActionMethod<T> = (dispatch: Dispatch<any>) => Promise<T>;

export interface PayloadAction<TType, TPayload> extends Action<TType> {
    payload: TPayload;
}

export function createAction<TAction extends Action<TAction["type"]>>(type: TAction["type"]): () => Action<TAction["type"]> {
    return () => ({
        type,
    });
}

export function createPayloadAction<TAction extends PayloadAction<TAction["type"], TAction["payload"]>>(type: TAction["type"]): (payload: TAction["payload"]) => PayloadAction<TAction["type"], TAction["payload"]> {
    return (payload: TAction["payload"]) => ({
        type,
        payload,
    });
}

export type BoundActionMethod<A = any, R = unknown> = (...args: A[]) => Promise<R>;
export type BoundActionsMapObject = { [key: string]: BoundActionMethod }

function bindActionCreator<A extends AnyAction = AnyAction>(actionCreator: ActionCreator<A>, dispatch: Dispatch<A>): BoundActionMethod {
    return async function (this: any, ...args: any[]) {
        const actionMethod = actionCreator.apply(this, args) as any as ActionMethod<A>;
        return await actionMethod(dispatch);
    }
}

export function bindActionCreators(
    actionCreators: ActionCreator<any> | ActionCreatorsMapObject,
    dispatch: Dispatch<any>
): BoundActionsMapObject | BoundActionMethod {
    if (typeof actionCreators === 'function') {
        return bindActionCreator(actionCreators, dispatch)
    }

    if (typeof actionCreators !== 'object' || actionCreators === null) {
        throw new Error('bindActionCreators expected an object or a function, did you write "import ActionCreators from" instead of "import * as ActionCreators from"?')
    }

    const boundActionCreators: ActionCreatorsMapObject = {}
    for (const key in actionCreators) {
        const actionCreator = actionCreators[key]
        if (typeof actionCreator === 'function') {
            boundActionCreators[key] = bindActionCreator(actionCreator, dispatch)
        }
    }
    return boundActionCreators
}