import React, { FC, ReactElement, useContext, useEffect, useMemo } from 'react';
import Header from './header';
import Sidebar from './sidebar';
import { Routes, Route, useNavigate } from 'react-router-dom';
import HomePage from '../pages/homePage';
import { Stack } from '@fluentui/react';
import { AppContext } from '../models/applicationState';
import { TodoContext } from '../components/todoContext';
import * as itemActions from '../actions/itemActions';
import * as listActions from '../actions/listActions';
import { ListActions } from '../actions/listActions';
import { ItemActions } from '../actions/itemActions';
import { TodoItem, TodoList } from '../models';
import { headerStackStyles, mainStackStyles, rootStackStyles, sidebarStackStyles } from '../ux/styles';
import TodoItemDetailPane from '../components/todoItemDetailPane';
import { bindActionCreators } from '../actions/actionCreators';

const Layout: FC = (): ReactElement => {
    const navigate = useNavigate();
    const appContext = useContext<AppContext>(TodoContext)
    const actions = useMemo(() => ({
        lists: bindActionCreators(listActions, appContext.dispatch) as unknown as ListActions,
        items: bindActionCreators(itemActions, appContext.dispatch) as unknown as ItemActions,
    }), [appContext.dispatch]);

    // Load initial lists
    useEffect(() => {
        if (!appContext.state.lists) {
            actions.lists.list();
        }
    }, [actions.lists, appContext.state.lists]);

    const onListCreated = async (list: TodoList) => {
        const newList = await actions.lists.save(list);
        navigate(`/lists/${newList.id}`);
    }

    const onItemEdited = (item: TodoItem) => {
        actions.items.save(item.listId, item);
        actions.items.select(undefined);
        navigate(`/lists/${item.listId}`);
    }

    const onItemEditCancel = () => {
        if (appContext.state.selectedList) {
            actions.items.select(undefined);
            navigate(`/lists/${appContext.state.selectedList.id}`);
        }
    }

    return (
        <Stack styles={rootStackStyles}>
            <Stack.Item styles={headerStackStyles}>
                <Header></Header>
            </Stack.Item>
            <Stack horizontal grow={1}>
                <Stack.Item styles={sidebarStackStyles}>
                    <Sidebar
                        selectedList={appContext.state.selectedList}
                        lists={appContext.state.lists}
                        onListCreate={onListCreated} />
                </Stack.Item>
                <Stack.Item grow={1} styles={mainStackStyles}>
                    <Routes>
                        <Route path="/lists/:listId/items/:itemId" element={<HomePage />} />
                        <Route path="/lists/:listId" element={<HomePage />} />
                        <Route path="/lists" element={<HomePage />} />
                        <Route path="/" element={<HomePage />} />
                    </Routes>
                </Stack.Item>
                <Stack.Item styles={sidebarStackStyles}>
                    <TodoItemDetailPane
                        item={appContext.state.selectedItem}
                        onEdit={onItemEdited}
                        onCancel={onItemEditCancel} />
                </Stack.Item>
            </Stack>
        </Stack>
    );
}

export default Layout;
