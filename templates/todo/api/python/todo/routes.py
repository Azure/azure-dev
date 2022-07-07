from datetime import datetime
from http import HTTPStatus
from typing import List, Optional
from urllib.parse import urljoin

from beanie import PydanticObjectId
from fastapi import HTTPException, Response
from starlette.requests import Request

from .app import app
from .models import (CreateUpdateTodoItem, CreateUpdateTodoList, TodoItem,
                     TodoList, TodoState)


@app.get("/lists", response_model=List[TodoList], response_model_by_alias=False)
async def get_lists(
    top: Optional[int] = None, skip: Optional[int] = None
) -> List[TodoList]:
    """
    Get all Todo lists

    Optional arguments:

    - **top**: Number of lists to return
    - **skip**: Number of lists to skip
    """
    query = TodoList.all().skip(skip).limit(top)
    return await query.to_list()


@app.post("/lists", response_model=TodoList, response_model_by_alias=False, status_code=201)
async def create_list(body: CreateUpdateTodoList, request: Request, response: Response) -> TodoList:
    """
    Create a new Todo list
    """
    todo_list = await TodoList(**body.dict(), createdDate=datetime.utcnow()).save()
    response.headers["Location"] = urljoin(str(request.base_url), "lists/{0}".format(str(todo_list.id)))
    return todo_list


@app.get("/lists/{list_id}", response_model=TodoList, response_model_by_alias=False)
async def get_list(list_id: PydanticObjectId) -> TodoList:
    """
    Get Todo list by ID
    """
    todo_list = await TodoList.get(document_id=list_id)
    if not todo_list:
        raise HTTPException(status_code=404, detail="Todo list not found")
    return todo_list


@app.put("/lists/{list_id}", response_model=TodoList, response_model_by_alias=False)
async def update_list(
    list_id: PydanticObjectId, body: CreateUpdateTodoList
) -> TodoList:
    """
    Updates a Todo list by unique identifier
    """
    todo_list = await TodoList.get(document_id=list_id)
    if not todo_list:
        raise HTTPException(status_code=404, detail="Todo list not found")
    await todo_list.update({"$set": body.dict(exclude_unset=True)})
    todo_list.updatedDate = datetime.utcnow()
    return await todo_list.save()


@app.delete("/lists/{list_id}", response_class=Response, status_code=204)
async def delete_list(list_id: PydanticObjectId) -> None:
    """
    Deletes a Todo list by unique identifier
    """
    todo_list = await TodoList.get(document_id=list_id)
    if not todo_list:
        raise HTTPException(status_code=404, detail="Todo list not found")
    await todo_list.delete()


@app.post("/lists/{list_id}/items", response_model=TodoItem, response_model_by_alias=False, status_code=201)
async def create_list_item(
    list_id: PydanticObjectId, body: CreateUpdateTodoItem, request: Request, response: Response
) -> TodoItem:
    """
    Creates a new Todo item within a list
    """
    item = TodoItem(listId=list_id, **body.dict(), createdDate=datetime.utcnow())
    response.headers["Location"] = urljoin(str(request.base_url), "lists/{0}/items/{1}".format(str(list_id), str(item.id)))
    return await item.save()


@app.get("/lists/{list_id}/items", response_model=List[TodoItem], response_model_by_alias=False)
async def get_list_items(
    list_id: PydanticObjectId,
    top: Optional[int] = None,
    skip: Optional[int] = None,
) -> List[TodoItem]:
    """
    Gets Todo items within the specified list

    Optional arguments:

    - **top**: Number of lists to return
    - **skip**: Number of lists to skip
    """
    query = TodoItem.find(TodoItem.listId == list_id).skip(skip).limit(top)
    return await query.to_list()


@app.get("/lists/{list_id}/items/state/{state}", response_model=List[TodoItem], response_model_by_alias=False)
async def get_list_items_by_state(
    list_id: PydanticObjectId,
    state: TodoState = ...,
    top: Optional[int] = None,
    skip: Optional[int] = None,
) -> List[TodoItem]:
    """
    Gets a list of Todo items of a specific state

    Optional arguments:

    - **top**: Number of lists to return
    - **skip**: Number of lists to skip
    """
    query = (
        TodoItem.find(TodoItem.listId == list_id, TodoItem.state == state)
        .skip(skip)
        .limit(top)
    )
    return await query.to_list()


@app.put("/lists/{list_id}/items/state/{state}", response_model=List[TodoItem], response_model_by_alias=False)
async def update_list_items_state(
    list_id: PydanticObjectId,
    state: TodoState = ...,
    body: List[str] = None,
) -> List[TodoItem]:
    """
    Changes the state of the specified list items
    """
    if not body:
        raise HTTPException(status_code=400, detail="No items specified")
    results = []    
    for id_ in body:
        item = await TodoItem.get(document_id=id_)
        if not item:
            raise HTTPException(status_code=404, detail="Todo item not found")
        item.state = state
        item.updatedDate = datetime.utcnow()
        results.append(await item.save())
    return results


@app.get("/lists/{list_id}/items/{item_id}", response_model=TodoItem, response_model_by_alias=False)
async def get_list_item(
    list_id: PydanticObjectId, item_id: PydanticObjectId
) -> TodoItem:
    """
    Gets a Todo item by unique identifier
    """
    item = await TodoItem.find_one(TodoItem.listId == list_id, TodoItem.id == item_id)
    if not item:
        raise HTTPException(status_code=404, detail="Todo item not found")
    return item


@app.put("/lists/{list_id}/items/{item_id}", response_model=TodoItem, response_model_by_alias=False)
async def update_list_item(
    list_id: PydanticObjectId,
    item_id: PydanticObjectId,
    body: CreateUpdateTodoItem,
) -> TodoItem:
    """
    Updates a Todo item by unique identifier
    """
    item = await TodoItem.find_one(TodoItem.listId == list_id, TodoItem.id == item_id)
    if not item:
        raise HTTPException(status_code=404, detail="Todo item not found")
    await item.update({"$set": body.dict(exclude_unset=True)})
    item.updatedDate = datetime.utcnow()
    return await item.save()


@app.delete("/lists/{list_id}/items/{item_id}", response_class=Response, status_code=204)
async def delete_list_item(
    list_id: PydanticObjectId, item_id: PydanticObjectId
) -> None:
    """
    Deletes a Todo item by unique identifier
    """
    todo_item = await TodoItem.find_one(TodoItem.id == item_id)
    if not todo_item:
        raise HTTPException(status_code=404, detail="Todo item not found")
    await todo_item.delete()
