import { useEffect, useContext, useMemo, useState, Fragment } from 'react';
import { IconButton, IContextualMenuProps, IIconProps, Stack, Text, Shimmer, ShimmerElementType } from '@fluentui/react';
import TodoItemListPane from '../components/todoItemListPane';
import { TodoItem, TodoItemState } from '../models';
import * as itemActions from '../actions/itemActions';
import * as listActions from '../actions/listActions';
import { TodoContext } from '../components/todoContext';
import { AppContext } from '../models/applicationState';
import { ItemActions } from '../actions/itemActions';
import { ListActions } from '../actions/listActions';
import { stackItemPadding, stackPadding, titleStackStyles } from '../ux/styles';
import { useNavigate, useParams } from 'react-router-dom';
import { bindActionCreators } from '../actions/actionCreators';
import WithApplicationInsights from '../components/telemetryWithAppInsights.tsx';

const HomePage = () => {
    const navigate = useNavigate();
    const appContext = useContext<AppContext>(TodoContext)
    const { listId, itemId } = useParams();
    const actions = useMemo(() => ({
        lists: bindActionCreators(listActions, appContext.dispatch) as unknown as ListActions,
        items: bindActionCreators(itemActions, appContext.dispatch) as unknown as ItemActions,
    }), [appContext.dispatch]);

    const [isReady, setIsReady] = useState(false)

    // Create default list of does not exist
    useEffect(() => {
        if (appContext.state.lists?.length === 0) {
            actions.lists.save({ name: 'My List' });
        }
    }, [actions.lists, appContext.state.lists?.length])

    // Select default list on initial load
    useEffect(() => {
        if (appContext.state.lists?.length && !listId && !appContext.state.selectedList) {
            const defaultList = appContext.state.lists[0];
            navigate(`/lists/${defaultList.id}`);
        }
    }, [appContext.state.lists, appContext.state.selectedList, listId, navigate])

    // React to selected list changes
    useEffect(() => {
        if (listId && appContext.state.selectedList?.id !== listId) {
            actions.lists.load(listId);
        }
    }, [actions.lists, appContext.state.selectedList, listId])

    // React to selected item change
    useEffect(() => {
        if (listId && itemId && appContext.state.selectedItem?.id !== itemId) {
            actions.items.load(listId, itemId);
        }
    }, [actions.items, appContext.state.selectedItem?.id, itemId, listId])

    // Load items for selected list
    useEffect(() => {
        if (appContext.state.selectedList?.id && !appContext.state.selectedList.items) {
            const loadListItems = async (listId: string) => {
                await actions.items.list(listId);
                setIsReady(true)
            }

            loadListItems(appContext.state.selectedList.id)
        }
    }, [actions.items, appContext.state.selectedList?.id, appContext.state.selectedList?.items])

    const onItemCreated = async (item: TodoItem) => {
        return await actions.items.save(item.listId, item);
    }

    const onItemCompleted = (item: TodoItem) => {
        item.state = TodoItemState.Done;
        item.completedDate = new Date();
        actions.items.save(item.listId, item);
    }

    const onItemSelected = (item?: TodoItem) => {
        actions.items.select(item);
    }

    const onItemDeleted = (item: TodoItem) => {
        if (item.id) {
            actions.items.remove(item.listId, item);
            navigate(`/lists/${item.listId}`);
        }
    }

    const deleteList = () => {
        if (appContext.state.selectedList?.id) {
            actions.lists.remove(appContext.state.selectedList.id);
            navigate('/lists');
        }
    }

    const iconProps: IIconProps = {
        iconName: 'More',
        styles: {
            root: {
                fontSize: 14
            }
        }
    }

    const menuProps: IContextualMenuProps = {
        items: [
            {
                key: 'delete',
                text: 'Delete List',
                iconProps: { iconName: 'Delete' },
                onClick: () => { deleteList() }
            }
        ]
    }

    return (
        <Stack>
            <Stack.Item>
                <Stack horizontal styles={titleStackStyles} tokens={stackPadding}>
                    <Stack.Item grow={1}>
                        <Shimmer width={300}
                            isDataLoaded={!!appContext.state.selectedList}
                            shimmerElements={
                                [
                                    { type: ShimmerElementType.line, height: 20 }
                                ]
                            } >
                            <Fragment>
                                <Text block variant="xLarge">{appContext.state.selectedList?.name}</Text>
                                <Text variant="small">{appContext.state.selectedList?.description}</Text>
                            </Fragment>
                        </Shimmer>
                    </Stack.Item>
                    <Stack.Item>
                        <IconButton
                            disabled={!isReady}
                            menuProps={menuProps}
                            iconProps={iconProps}
                            styles={{ root: { fontSize: 16 } }}
                            title="List Actions"
                            ariaLabel="List Actions" />
                    </Stack.Item>
                </Stack>
            </Stack.Item>
            <Stack.Item tokens={stackItemPadding}>
                <TodoItemListPane
                    list={appContext.state.selectedList}
                    items={appContext.state.selectedList?.items}
                    selectedItem={appContext.state.selectedItem}
                    disabled={!isReady}
                    onSelect={onItemSelected}
                    onCreated={onItemCreated}
                    onComplete={onItemCompleted}
                    onDelete={onItemDeleted} />
            </Stack.Item>
        </Stack >
    );
};

const HomePageWithTelemetry = WithApplicationInsights(HomePage, 'HomePage');

export default HomePageWithTelemetry;
