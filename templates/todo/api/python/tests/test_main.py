def test_list(app_client):
    # Create 2 test locations
    first = app_client.post(
            "/lists",
            json={
                "name": "Test List 1",
                "description": "My first test list",
            },
        )
    assert first.status_code == 201
    assert first.headers["Location"].startswith("http://testserver/lists/")
    assert (
        app_client.post(
            "/lists",
            json={
                "name": "Test List 2",
                "description": "My second test list",
            },
        ).status_code
        == 201
    )

    # Get all lists
    response = app_client.get("/lists")
    assert response.status_code == 200
    result = response.json()
    assert len(result) == 2
    assert result[0]["name"] == "Test List 1"
    assert result[1]["name"] == "Test List 2"
    assert result[0]["createdDate"] is not None
    assert result[1]["createdDate"] is not None

    # Test those lists at the ID URL
    assert result[0]["id"] is not None
    test_list_id = result[0]["id"]
    test_list_id2 = result[1]["id"]

    response = app_client.get("/lists/{0}".format(test_list_id))
    assert response.status_code == 200
    result = response.json()
    assert result["name"] == "Test List 1"
    assert result["description"] == "My first test list"

    # Test a list with a bad ID
    response = app_client.get("/lists/{0}".format("61958439e0dbd854f5ab9000"))
    assert response.status_code == 404

    # Update a list
    response = app_client.put(
        "/lists/{0}".format(test_list_id),
        json={
            "name": "Test List 1 Updated",
        },
    )
    assert response.status_code == 200
    result = response.json()
    assert result["name"] == "Test List 1 Updated"
    assert result["updatedDate"] is not None

    # Delete a list
    response = app_client.delete("/lists/{0}".format(test_list_id2))
    assert response.status_code == 204
    response = app_client.get("/lists/{0}".format(test_list_id2))
    assert response.status_code == 404

    # Create a list item
    response = app_client.post(
        "/lists/{0}/items".format(test_list_id),
        json={
            "name": "Test Item 1",
            "description": "My first test item",
        },
    )
    assert response.status_code == 201
    assert response.headers["Location"].startswith("http://testserver/lists/")
    assert "items/" in response.headers["Location"]


    # Get all list items
    response = app_client.get("/lists/{0}/items".format(test_list_id))
    assert response.status_code == 200
    result = response.json()
    assert len(result) == 1
    assert result[0]["name"] == "Test Item 1"
    assert result[0]["description"] == "My first test item"
    assert result[0]["createdDate"] is not None
    test_item_id = result[0]["id"]

    # Update list item
    response = app_client.put(
        "/lists/{0}/items/{1}".format(test_list_id, test_item_id),
        json={
            "name": "Test Item 1 Updated",
            "description": "My first test item",
            "state": "done",
        },
    )
    assert response.status_code == 200, response.text

    # Get list item by id
    response = app_client.get("/lists/{0}/items/{1}".format(test_list_id, test_item_id))
    assert response.status_code == 200
    result = response.json()
    assert result["name"] == "Test Item 1 Updated"
    assert result["state"] == "done"
    assert result["updatedDate"] is not None

    # Get list items by state
    response = app_client.get("/lists/{0}/items/state/done".format(test_list_id))
    assert response.status_code == 200
    result = response.json()
    assert len(result) == 1
    assert result[0]["name"] == "Test Item 1 Updated"
    assert result[0]["state"] == "done"

    # Delete list item
    response = app_client.delete(
        "/lists/{0}/items/{1}".format(test_list_id, test_item_id)
    )
    assert response.status_code == 204
