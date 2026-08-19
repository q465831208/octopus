import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';
import { groupListQueryOptions } from './queries';

// GroupItem 是分组内可手动选择的渠道模型。
export interface GroupItem {
    id?: number;
    group_id?: number;
    channel_id: number;
    model_name: string;
    priority: number;
    // enabled 是否参与负载均衡；false=被健康测试标记禁用(不删除，仅跳过)。缺省按启用处理。
    enabled?: boolean;
}

// Group 是客户端模型名称对应的手动渠道分组。
export interface Group {
    id?: number;
    name: string;
    active_item_id: number;
    retry_interval: number;
    items?: GroupItem[];
}

// GroupItemAddRequest 是待新增的分组项。
interface GroupItemAddRequest {
    channel_id: number;
    model_name: string;
    priority: number;
}

// GroupItemUpdateRequest 是待更新展示顺序的分组项。
interface GroupItemUpdateRequest {
    id: number;
    priority: number;
}

// GroupUpdateRequest 是分组普通配置和成员变更。
export interface GroupUpdateRequest {
    id: number;
    name?: string;
    retry_interval?: number;
    items_to_add?: GroupItemAddRequest[];
    items_to_update?: GroupItemUpdateRequest[];
    items_to_delete?: number[];
}

// GroupItemTestResult 是分组内单项模型对话测试的结果。
export interface GroupItemTestResult {
    ok: boolean;
    latency_ms: number;
    detail: string;
    preview: string;
}

// GroupItemTestOutcome 是批量测试时单个项的结果（含 item_id / channel / model）。
export interface GroupItemTestOutcome extends GroupItemTestResult {
    item_id: number;
    channel_id: number;
    model_name: string;
}

// GroupTestAllResult 是一次性测试全部分组项的结果（失败项已自动移出分组）。
export interface GroupTestAllResult {
    tested: number;
    kept: number;
    removed: number;
    results: GroupItemTestOutcome[];
}

// testGroupItem 测试分组内 (channel_id, model_name) 能否正常对话。
export function testGroupItem(channelId: number, modelName: string): Promise<GroupItemTestResult> {
    return apiRequest<GroupItemTestResult>('/api/v1/group/test-item', {
        method: 'POST',
        body: { channel_id: channelId, model_name: modelName },
    });
}

// testGroupAll 一次性测试分组内所有模型项；失败的项会被自动移出分组，只在可用项上负载均衡。
export function testGroupAll(groupId: number): Promise<GroupTestAllResult> {
    return apiRequest<GroupTestAllResult>('/api/v1/group/test-all', {
        method: 'POST',
        body: { group_id: groupId },
    });
}

// useGroupList 获取全部分组，可由调用方控制是否立即查询。
export function useGroupList(enabled = true) {
    return useQuery({
        ...groupListQueryOptions,
        enabled,
        refetchInterval: 30000,
        refetchOnMount: 'always',
    });
}

// useCreateGroup 创建分组。
export function useCreateGroup() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (data: Group) =>
            apiRequest<Group>('/api/v1/group/create', { method: 'POST', body: data }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: groupListQueryOptions.queryKey }),
    });
}

// useUpdateGroup 更新分组配置和成员。
export function useUpdateGroup() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (data: GroupUpdateRequest) =>
            apiRequest<Group>('/api/v1/group/update', { method: 'POST', body: data }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: groupListQueryOptions.queryKey }),
    });
}

// useUpdateGroupActiveItem 手动切换或清空分组当前渠道。
export function useUpdateGroupActiveItem() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ groupId, itemId }: { groupId: number; itemId: number }) =>
            apiRequest<Group>(`/api/v1/group/active/${groupId}`, {
                method: 'POST',
                body: { item_id: itemId },
            }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: groupListQueryOptions.queryKey }),
    });
}

// useDeleteGroup 删除分组。
export function useDeleteGroup() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<null>(`/api/v1/group/delete/${id}`, { method: 'DELETE' }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: groupListQueryOptions.queryKey }),
    });
}
