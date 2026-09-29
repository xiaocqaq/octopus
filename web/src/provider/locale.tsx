import { useEffect, type ReactNode } from 'react';
import { IntlProvider } from 'use-intl';
import { useSettingStore, type Locale } from '@/stores/setting';

import zh_hansMessages from '@/locales/zh_hans.json';
import zh_hantMessages from '@/locales/zh_hant.json';
import enMessages from '@/locales/en.json';
// 模型监控的文案单独成文件: 这几个键值属于一个独立功能, 混进那三个动辄数百行的大文件里
// 既容易在合并上游时冲突, 也看不出它们是一套。
import scheduledProbeZhHans from '@/locales/scheduled-probe.zh_hans.json';
import scheduledProbeZhHant from '@/locales/scheduled-probe.zh_hant.json';
import scheduledProbeEn from '@/locales/scheduled-probe.en.json';

// mergeMessages 把功能自带的文案并进主文案表; 命名空间互不重叠, 浅合并即可。
function mergeMessages(base: typeof zh_hansMessages, extra: Record<string, unknown>) {
    return { ...base, ...extra } as typeof zh_hansMessages;
}

const messages: Record<Locale, typeof zh_hansMessages> = {
    zh_hans: mergeMessages(zh_hansMessages, { scheduledProbe: scheduledProbeZhHans }),
    zh_hant: mergeMessages(zh_hantMessages, { scheduledProbe: scheduledProbeZhHant }),
    en: mergeMessages(enMessages, { scheduledProbe: scheduledProbeEn }),
};

const languageTags: Record<Locale, string> = {
    zh_hans: 'zh-Hans',
    zh_hant: 'zh-Hant',
    en: 'en',
};

export function LocaleProvider({ children }: { children: ReactNode }) {
    const locale = useSettingStore((state) => state.locale);

    useEffect(() => {
        document.documentElement.lang = languageTags[locale];
    }, [locale]);

    return (
        <IntlProvider
            locale={languageTags[locale]}
            messages={messages[locale]}
            timeZone="Asia/Shanghai"
        >
            {children}
        </IntlProvider>
    );
}
